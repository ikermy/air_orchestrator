// Package mcp реализует MCP-сервер по стандарту 2025-03-26 (Streamable HTTP transport).
package mcp

import (
	storageusecase "air_orchestrator/internal/usecase/storage"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/comdom"
	"github.com/ikermy/air-common/v2/pkg/mcpsession"
	"github.com/ikermy/air-logger/v2/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var tracer = otel.Tracer("air_orchestrator/mcp")

// Store — интерфейс к БД для MCP-хендлера.
// Объединяет comdb.Exterior (google_services) и AppConfigRepository (чтение настроек).
type Store interface {
	comdb.Exterior
	GetAppConfig(ctx context.Context, key string) (string, error)
	UserOwnsResponder(userID uint64, responderID uint64) (bool, error)
	WriteAuditLog(ctx context.Context, userID uint64, action string, details json.RawMessage) error
}

// ModelStore — интерфейс к хранилищу моделей пользователей.
type ModelStore interface {
	GetUserModels(userId uint64) ([]comdom.UniversalModelData, error)
}

// LeadTargetFn — тип колбэка для вызова Meta-сервиса lead/target.
// Инжектируется из web через SetLeadTargetFn чтобы избежать дублирования HTTP-логики.
type LeadTargetFn func(ctx context.Context, respId int64) error

// Handler — MCP-сервер.
type Handler struct {
	store         Store
	mod           ModelStore
	ctx           context.Context
	cancel        context.CancelFunc
	leadTargetFn  LeadTargetFn // опционально; если nil — используется leadTargetClient напрямую
	files         *storageusecase.Service
	sessionSecret string
	requireSigned bool
	serviceKey    string

	limiterMu sync.Mutex
	limiters  map[string]*toolLimiterEntry

	idemMu sync.Mutex
	idem   map[string]idempotencyEntry
}

// New создаёт новый MCP-хендлер с собственным дочерним контекстом.
func New(parent context.Context, db Store, mod ModelStore, files ...*storageusecase.Service) *Handler {
	ctx, cancel := context.WithCancel(parent)
	var fileService *storageusecase.Service
	if len(files) > 0 {
		fileService = files[0]
	}
	return &Handler{store: db, mod: mod, ctx: ctx, cancel: cancel, files: fileService}
}

// SetLeadTargetFn инжектирует функцию вызова Meta-сервиса.
// Вызывается из app.go после создания web.web.
func (h *Handler) SetLeadTargetFn(fn LeadTargetFn) {
	h.leadTargetFn = fn
}

// SetSessionAuth настраивает проверку X-Session-ID.
// secret включает проверку подписи для подписанных заголовков;
// requireSigned запрещает legacy-формат "userId:provider".
func (h *Handler) SetSessionAuth(secret string, requireSigned bool) {
	h.sessionSecret = secret
	h.requireSigned = requireSigned
	if requireSigned && secret == "" {
		logger.Error("MCP: MCP_REQUIRE_SIGNED_SESSION=true, но MCP_SESSION_SECRET пуст — запросы будут отклонены", 0)
	}
}

// SetServiceAuth включает проверку заголовка X-Service-Key.
// Пустой ключ отключает проверку.
func (h *Handler) SetServiceAuth(key string) {
	h.serviceKey = key
}

func (h *Handler) checkServiceKey(provided string) bool {
	if h.serviceKey == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.serviceKey)) == 1
}

// authenticate разбирает и, при необходимости, проверяет X-Session-ID.
func (h *Handler) authenticate(header string) (uint64, comdom.ProviderType, error) {
	if h.requireSigned {
		if h.sessionSecret == "" {
			return 0, 0, errors.New("server session secret is not configured")
		}
		if !mcpsession.IsSigned(header) {
			return 0, 0, errors.New("signed session required")
		}
		return mcpsession.Parse(h.sessionSecret, header, time.Now())
	}
	if h.sessionSecret != "" && mcpsession.IsSigned(header) {
		return mcpsession.Parse(h.sessionSecret, header, time.Now())
	}
	return ParseSessionID(header)
}

// Shutdown останавливает MCP-сервер, отменяя его контекст.
func (h *Handler) Shutdown() {
	if h.cancel != nil {
		h.cancel()
	}
	logger.Info("MCP: сервер остановлен")
}

// ========== JSON-RPC 2.0 типы ==========

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeHTTP — точка входа, вызывается из gin-роута POST /mcp.
func (h *Handler) ServeHTTP(c *gin.Context) {
	var req rpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		c.JSON(http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32600, Message: "Invalid Request"},
		})
		return
	}

	// Уведомления (нет поля "id") — HTTP 202, тело пустое
	if req.ID == nil {
		logger.Debug("MCP: уведомление '%s' получено", req.Method)
		c.Status(http.StatusAccepted)
		return
	}

	id := *req.ID

	if !h.checkServiceKey(c.GetHeader("X-Service-Key")) {
		c.JSON(http.StatusUnauthorized, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32001, Message: "Unauthorized: invalid service key"},
		})
		return
	}

	ctx, span := tracer.Start(c.Request.Context(), "mcp.request")
	defer span.End()
	span.SetAttributes(attribute.String("mcp.method", req.Method))
	c.Request = c.Request.WithContext(ctx)

	// initialize не требует X-Session-ID
	if req.Method == "initialize" {
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo": map[string]any{
					"name":    "AiR-Landing-MCP",
					"version": "1.0.0",
				},
				"capabilities": map[string]any{
					"tools":   map[string]any{},
					"prompts": map[string]any{},
				},
			},
		})
		return
	}

	// Все остальные методы требуют X-Session-ID
	userId, provider, err := h.authenticate(c.Request.Header.Get("X-Session-ID"))
	if err != nil {
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32001, Message: "Unauthorized: " + err.Error()},
		})
		return
	}

	switch req.Method {
	case "tools/list":
		tools, err := h.buildToolsList(userId, provider)
		if err != nil {
			logger.ErrorCtx(ctx, "MCP tools/list: %v", err, userId)
			c.JSON(http.StatusOK, rpcResponse{
				JSONRPC: "2.0",
				ID:      id,
				Error:   &rpcError{Code: -32603, Message: "Internal error"},
			})
			return
		}
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  map[string]any{"tools": tools},
		})

	case "tools/call":
		result := h.callTool(c.Request.Context(), req.Params, userId, provider)
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  result,
		})

	case "prompts/list":
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string]any{
				"prompts": []map[string]string{
					{
						"name":        "system",
						"description": "System prompt hint based on user model configuration",
					},
				},
			},
		})

	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		if len(req.Params) == 0 || json.Unmarshal(req.Params, &p) != nil || p.Name != "system" {
			c.JSON(http.StatusOK, rpcResponse{
				JSONRPC: "2.0",
				ID:      id,
				Error:   &rpcError{Code: -32602, Message: "Invalid params: only 'system' prompt is supported"},
			})
			return
		}
		hint := h.buildSystemPromptHint(userId, provider)
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string]any{
				"description": "System prompt hint for AI model",
				"messages": []map[string]any{
					{
						"role": "assistant",
						"content": map[string]string{
							"type": "text",
							"text": hint,
						},
					},
				},
			},
		})

	default:
		c.JSON(http.StatusOK, rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32601, Message: "Method not found"},
		})
	}
}
