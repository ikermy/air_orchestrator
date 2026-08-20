// Package grpc implements the internal gRPC server for inter-service communication.
//
// The server provides ConfigService — an API for obtaining decrypted sensitive
// configurations (e.g. Telegram bot token, User MasterKey) by other microservices.
//
// Authorization: metadata header "x-service-key" must contain the service key
// stored in app_config under "svc.service_key".
//
// Transport: plain gRPC inside Docker network (TLS terminated by Envoy on :50443).
// Direct TLS is supported via GRPC_CERT_FILE / GRPC_KEY_FILE environment variables.
package grpc

import (
	callspb "air_orchestrator/internal/delivery/grpc/v1"
	"air_orchestrator/internal/domain/repository"
	"air_orchestrator/internal/domain/state"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ikermy/air_common/pkg/rpc/proto"
	"github.com/ikermy/air_logger/v2/pkg/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ExamProvider is the minimal interface for reading methods from the security package.
// It is used by the gRPC server to avoid importing the security package from the delivery layer.
// Implemented by *exam.Exam; injected into the gRPC server to avoid importing the
// security package from the delivery layer.
type ExamProvider interface {
	GetMasterKey(userId uint32) ([32]byte, bool)
	WidgetNewToken(userID uint32, respID uint64, origin string, expired time.Duration) (string, error)
	WidgetParseToken(tokenString string) (*proto.WidgetTokenData, error)
	WidgetNewCode(data *proto.WidgetCodeData, publicKey, privateKey string) (string, error)
	WidgetParseCode(token, publicKey string) (*proto.WidgetCodeData, error)
	WidgetParseExpiredToken(expiredTokenString string) (*proto.WidgetTokenData, error)
	VerifyAccessToken(token string) (uint32, uint64, error)
}

type widgetKeys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// configServer implements proto.ConfigServiceServer.
type configServer struct {
	proto.UnimplementedConfigServiceServer
	db         repository.AppConfig
	provider   ExamProvider
	serviceKey string
	widgetKeys widgetKeys
}

// Server manages the lifecycle of the gRPC server.
type Server struct {
	grpc   *grpc.Server
	listen net.Listener
}

// RegisterCallsService adds the public calls API to the existing gRPC server.
// It is intentionally separate from New so the internal ConfigService remains
// compatible with existing service-key clients.
func (s *Server) RegisterCallsService(service callspb.CallsServer) {
	callspb.RegisterCallsServer(s.grpc, service)
}

// New creates a gRPC server with service-key authorization interceptor.
// TLS is optional: if certFile and keyFile are non-empty — TLS is enabled.
// When running behind Envoy, pass empty strings — TLS is terminated at the Envoy level.
//
//   - db         — repository for reading app_config (decryption happens inside)
//   - mkProvider — MasterKey provider (exam.Exam); used by GetUserMasterKey
//   - serviceKey — secret that client microservices send in "x-service-key"
//   - certFile   — path to TLS server certificate (empty = plain gRPC)
//   - keyFile    — path to TLS server key (empty = plain gRPC)
//   - port       — listen port (e.g. "50051")
func New(db repository.AppConfig, mkProvider ExamProvider, serviceKey, port string) (*Server, error) {
	svc := &configServer{db: db, provider: mkProvider, serviceKey: serviceKey}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(serviceKeyInterceptor(serviceKey, mkProvider)),
		grpc.StreamInterceptor(streamAuthInterceptor(mkProvider)),
	}

	// При работе за Envoy сертификаты не нужны — plain gRPC, TLS на стороне Envoy.
	logger.Info("gRPC: plain mode — TLS terminated by Envoy/proxy")

	srv := grpc.NewServer(opts...)
	proto.RegisterConfigServiceServer(srv, svc)

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return nil, fmt.Errorf("gRPC listen :%s: %w", port, err)
	}

	return &Server{grpc: srv, listen: lis}, nil
}

// Start runs the gRPC server in blocking mode (call in a goroutine).
func (s *Server) Start() error {
	logger.Info("gRPC ConfigService started on %s", s.listen.Addr())
	return s.grpc.Serve(s.listen)
}

// Shutdown performs a graceful stop of the gRPC server.
func (s *Server) Shutdown() {
	s.grpc.GracefulStop()
	logger.Debug("gRPC server stopped")
}

// serviceKeyInterceptor — unary server interceptor that validates the service key.
// The key is passed in gRPC metadata under "x-service-key".
// Comparison uses subtle.ConstantTimeCompare to prevent timing attacks.
// If serviceKey is empty — always rejects (key not configured).
func serviceKeyInterceptor(serviceKey string, accessTokenProvider ExamProvider) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		logger.Debug("gRPC unary request: method=%s", info.FullMethod)
		if strings.HasPrefix(info.FullMethod, "/calls.v1.Calls/") {
			logger.Debug("gRPC calls auth: method=%s", info.FullMethod)
			ctx, err := authenticateBearer(ctx, accessTokenProvider)
			if err != nil {
				logger.Debug("gRPC calls auth failed: method=%s err=%v", info.FullMethod, err)
				return nil, err
			}
			resp, err := handler(ctx, req)
			if err != nil {
				logger.Debug("gRPC unary failed: method=%s err=%v", info.FullMethod, err)
			} else {
				logger.Debug("gRPC unary completed: method=%s", info.FullMethod)
			}
			return resp, err
		}
		if serviceKey == "" {
			return nil, status.Error(codes.Unavailable, "service key not configured")
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		vals := md.Get(state.GrpcKeyMeta)
		if len(vals) == 0 {
			logger.Warn("gRPC: request without x-service-key: %s", info.FullMethod)
			return nil, status.Error(codes.Unauthenticated, "missing service key")
		}
		if subtle.ConstantTimeCompare([]byte(vals[0]), []byte(serviceKey)) != 1 {
			logger.Warn("gRPC: invalid x-service-key: %s", info.FullMethod)
			return nil, status.Error(codes.PermissionDenied, "invalid service key")
		}
		resp, err := handler(ctx, req)
		if err != nil {
			logger.Debug("gRPC unary failed: method=%s err=%v", info.FullMethod, err)
		} else {
			logger.Debug("gRPC unary completed: method=%s", info.FullMethod)
		}
		return resp, err
	}
}

func streamAuthInterceptor(accessTokenProvider ExamProvider) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		logger.Debug("gRPC stream request: method=%s", info.FullMethod)
		if strings.HasPrefix(info.FullMethod, "/calls.v1.Calls/") {
			ctx, err := authenticateBearer(stream.Context(), accessTokenProvider)
			if err != nil {
				logger.Debug("gRPC stream auth failed: method=%s err=%v", info.FullMethod, err)
				return err
			}
			err = handler(srv, &wrappedServerStream{ServerStream: stream, ctx: ctx})
			if err != nil {
				logger.Debug("gRPC stream failed: method=%s err=%v", info.FullMethod, err)
			} else {
				logger.Debug("gRPC stream completed: method=%s", info.FullMethod)
			}
			return err
		}
		return status.Error(codes.Unauthenticated, "stream authentication is not configured")
	}
}

type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *wrappedServerStream) Context() context.Context { return s.ctx }

func authenticateBearer(ctx context.Context, provider ExamProvider) (context.Context, error) {
	if provider == nil {
		return nil, status.Error(codes.Unavailable, "access token verifier is not configured")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing bearer token")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return nil, status.Error(codes.Unauthenticated, "invalid bearer token")
	}
	userID, _, err := provider.VerifyAccessToken(parts[1])
	if err != nil || userID == 0 {
		return nil, status.Error(codes.Unauthenticated, "invalid bearer token")
	}
	return WithUserID(ctx, userID), nil
}

// GetBotConfig возвращает конфигурацию Telegram бота
func (s *configServer) GetBotConfig(ctx context.Context, _ *proto.GetBotConfigRequest) (*proto.BotConfigResponse, error) {
	// Получаем тот же токен и имя бота
	token, err := s.db.GetAppConfig(ctx, "tg.token")
	if err != nil {
		logger.Error("GetBotConfig: can't get tg.token: %v", err)
		return nil, status.Error(codes.Internal, "config error")
	}

	botName, err := s.db.GetAppConfig(ctx, "tg.bot")
	if err != nil {
		logger.Error("GetBotConfig: can't get tg.bot: %v", err)
		return nil, status.Error(codes.Internal, "config error")
	}

	return &proto.BotConfigResponse{
		Token:   token,
		BotName: botName,
	}, nil
}

// GetOperBotConfig возвращает конфигурацию Telegram бота (операторский).
func (s *configServer) GetOperBotConfig(ctx context.Context, _ *proto.GetBotConfigRequest) (*proto.BotConfigResponse, error) {
	// Получаем тот же токен и имя бота
	token, err := s.db.GetAppConfig(ctx, "oper.tg.token")
	if err != nil {
		logger.Error("GetOperBotConfig: can't get oper.tg.token: %v", err)
		return nil, status.Error(codes.Internal, "config error")
	}

	botName, err := s.db.GetAppConfig(ctx, "oper.tg.bot")
	if err != nil {
		logger.Error("GetOperBotConfig: can't get oper.tg.bot: %v", err)
		return nil, status.Error(codes.Internal, "config error")
	}

	return &proto.BotConfigResponse{
		Token:   token,
		BotName: botName,
	}, nil
}

func (s *configServer) GetUserMasterKey(_ context.Context, req *proto.GetUserMasterKeyRequest) (*proto.UserMasterKeyResponse, error) {
	masterKey, ok := s.provider.GetMasterKey(req.GetUserId())
	if !ok {
		logger.Warn("gRPC: can't get master key")
		return nil, status.Error(codes.NotFound, "master key not found")
	}

	return &proto.UserMasterKeyResponse{
		MasterKey: masterKey[:],
	}, nil
}

func (s *configServer) WidgetNewToken(_ context.Context, req *proto.WidgetTokenData) (*proto.WidgetRawToken, error) {
	widgetNewToken, err := s.provider.WidgetNewToken(req.UserId, req.RespId, req.Origin, time.Duration(req.ExpiredSeconds))
	if err != nil {
		logger.Error("WidgetNewToken: failed to generate widget token: %v", err, req.UserId)
		return nil, status.Errorf(codes.Internal, "failed to generate widget token: %v", err)
	}

	return &proto.WidgetRawToken{
		Token: widgetNewToken,
	}, nil
}

func (s *configServer) WidgetParseToken(_ context.Context, req *proto.WidgetRawToken) (*proto.WidgetTokenData, error) {
	tokenData, err := s.provider.WidgetParseToken(req.Token)
	if err != nil {
		logger.Error("WidgetParseToken: failed to parse widget token: %v", err)
		// Ошибка парсинга токена
		return nil, status.Errorf(codes.Unauthenticated, "invalid or expired token: %v", err)
	}

	return tokenData, nil
}

func (s *configServer) WidgetNewCode(ctx context.Context, req *proto.WidgetCodeData) (*proto.WidgetRawToken, error) {
	widgKeys, err := s.helperWidgetKeys(ctx)
	if err != nil {
		logger.Error("WidgetNewCode: failed to generate widget code keys: %v", err, req.UserId)
		return nil, status.Errorf(codes.Internal, "failed to get widget keys: %v", err)
	}

	widgetNewCode, err := s.provider.WidgetNewCode(
		req,
		widgKeys.Public,
		widgKeys.Private,
	)
	if err != nil {
		logger.Error("WidgetNewCode: failed to generate widget code: %v", err, req.UserId)
		return nil, status.Errorf(codes.Internal, "failed to generate widget code: %v", err)
	}

	return &proto.WidgetRawToken{
		Token: widgetNewCode,
	}, nil
}

func (s *configServer) helperWidgetKeys(ctx context.Context) (widgetKeys, error) {
	if s.widgetKeys.Private != "" && s.widgetKeys.Public != "" {
		return s.widgetKeys, nil
	}

	widgKeys, err := s.db.GetAppConfig(ctx, "widg.ed25519_keys")
	if err != nil {
		return widgetKeys{}, status.Errorf(codes.Internal, "failed to get widget keys: %v", err)
	}

	var keys widgetKeys
	if err = json.Unmarshal([]byte(widgKeys), &keys); err != nil {
		return widgetKeys{}, status.Errorf(codes.Internal, "failed to unmarshal widget keys: %v", err)
	}

	s.widgetKeys = keys

	return keys, nil
}

func (s *configServer) WidgetParseCode(ctx context.Context, req *proto.WidgetRawToken) (*proto.WidgetCodeData, error) {
	widgKeys, err := s.helperWidgetKeys(ctx)
	if err != nil {
		logger.Error("WidgetParseCode: failed to get widget keys: %v", err)
		return nil, status.Errorf(
			codes.Internal,
			"failed to get widget keys: %v",
			err,
		)
	}

	result, err := s.provider.WidgetParseCode(req.Token, widgKeys.Public)
	if err != nil {
		logger.Error("WidgetParseCode: failed to parse widget code: %v", err)
		return nil, status.Errorf(
			codes.Internal,
			"failed to parse widget code: %v",
			err,
		)
	}

	return result, nil
}

func (s *configServer) WidgetParseExpiredToken(_ context.Context, req *proto.WidgetRawToken) (*proto.WidgetTokenData, error) {
	result, err := s.provider.WidgetParseExpiredToken(req.Token)
	if err != nil {
		logger.Error(
			"WidgetParseExpiredToken: failed to parse token: %v",
			err,
		)

		return nil, status.Errorf(
			codes.Unauthenticated,
			"invalid or expired token: %v",
			err,
		)
	}

	return result, nil
}
