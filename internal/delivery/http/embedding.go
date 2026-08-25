package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-common/pkg/comdom"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// UploadEmbeddingRequest представляет запрос на загрузку документа с эмбеддингом.
type UploadEmbeddingRequest struct {
	Token    string                   `json:"token"`
	Provider string                   `json:"provider"` // "google" или "openai"
	DocName  string                   `json:"doc_name" binding:"required"`
	Content  string                   `json:"content" binding:"required"`
	Metadata *comdom.DocumentMetadata `json:"metadata,omitempty"`
}

// UploadEmbedding godoc
// @Summary Загрузить документ с эмбеддингом
// @Tags embedding
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Документ и провайдер"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /embedding/upload [post]
func (w *Web) UploadEmbedding(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем файл из запроса
	var req UploadEmbeddingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("UploadEmbedding: ошибка парсинга запроса: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Если метаданные не переданы — создаём базовые
	metadata := comdom.DocumentMetadata{
		Source:    "api_upload",
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if req.Metadata != nil {
		metadata = *req.Metadata
		if metadata.CreatedAt == "" {
			metadata.CreatedAt = time.Now().Format(time.RFC3339)
		}
	}

	logger.Debug("UploadEmbedding: вызов UploadDocumentWithEmbedding provider=%s, docName=%s", req.Provider, req.DocName, userId)
	docID, err := w.mod.UploadDocumentWithEmbedding(userId, req.Provider, req.DocName, req.Content, metadata)
	if err != nil {
		logger.Error("UploadEmbedding: ошибка при загрузке документа с эмбеддингом: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("UploadEmbedding: документ успешно загружен, docID=%s", docID, userId)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"doc_id":  docID,
		"message": "Document uploaded successfully with embedding",
	})
}

// ListUserDocuments godoc
// @Summary Получить список документов пользователя
// @Tags embedding
// @Produce json
// @Security BearerAuth
// @Param provider query string true "Тип провайдера (google/openai)"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /embedding/list [get]
func (w *Web) ListUserDocuments(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	provider := c.Query("provider")
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider query parameter is required (google/openai)"})
		return
	}

	documents, err := w.mod.ListUserDocuments(userId, provider)
	if err != nil {
		logger.Warn("ListUserDocuments: не удалось получить список документов (provider=%s): %v", provider, err, userId)
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"documents": []comdom.VectorDocument{},
			"count":     0,
			"provider":  provider,
		})
		return
	}

	if documents == nil {
		documents = []comdom.VectorDocument{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"documents": documents,
		"count":     len(documents),
		"provider":  provider,
	})
}

// DeleteDocument godoc
// @Summary Удалить документ
// @Tags embedding
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID документа"
// @Param provider query string true "Тип провайдера (google/openai)"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /embedding/{id} [delete]
func (w *Web) DeleteDocument(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	docID := c.Param("id")
	if docID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Document ID is required"})
		return
	}

	provider := c.Query("provider")
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider is required (google/openai)"})
		return
	}

	logger.Debug("DeleteDocument: удаление docID=%s, provider=%s", docID, provider, userId)
	if err := w.mod.DeleteDocument(userId, provider, docID); err != nil {
		logger.Error("DeleteDocument: ошибка при удалении документа %s (provider=%s): %v", docID, provider, err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "Document deleted successfully",
		"doc_id":   docID,
		"provider": provider,
	})
}
