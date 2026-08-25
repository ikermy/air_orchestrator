package grpc

import (
	"air_orchestrator/internal/delivery/grpc/v1"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ikermy/air-common/pkg/comdom"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// CallsProxy forwards the public Calls API to the platform-specific backend.
// The backend contract is identical for WhatsApp and Telegram.
type CallsProxy struct {
	v1.UnimplementedCallsServer
	whatsapp v1.CallsClient
	telegram v1.CallsClient
	model    ActiveModelProvider
	db       DB
	calls    sync.Map // call_id -> callspb.CallProvider
	conns    []*grpc.ClientConn
}

type ActiveModelProvider interface {
	GetActiveUserModel(userID uint32) (*comdom.UniversalModelData, error)
}

type DB interface {
	CheckDemo(userID uint32) (bool, error)
}

func NewCallsProxy(whatsapp, telegram v1.CallsClient, demo DB) *CallsProxy {
	return &CallsProxy{whatsapp: whatsapp, telegram: telegram, db: demo}
}

func (p *CallsProxy) SetModelProvider(provider ActiveModelProvider) {
	p.model = provider
}

func DialCallsBackends(whatsappAddr, telegramAddr string, demo DB) (*CallsProxy, error) {
	wConn, err := grpc.NewClient(whatsappAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial WhatsApp calls backend: %w", err)
	}
	tConn, err := grpc.NewClient(telegramAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = wConn.Close()
		return nil, fmt.Errorf("dial Telegram calls backend: %w", err)
	}
	proxy := NewCallsProxy(v1.NewCallsClient(wConn), v1.NewCallsClient(tConn), demo)
	proxy.conns = []*grpc.ClientConn{wConn, tConn}
	return proxy, nil
}

func (p *CallsProxy) Shutdown() error {
	var closeErr error
	for _, conn := range p.conns {
		if err := conn.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func (p *CallsProxy) StartOutgoingCall(ctx context.Context, req *v1.StartOutgoingCallRequest) (*v1.StartOutgoingCallResponse, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	backend, err := p.backend(req.GetProvider())
	if err != nil {
		return nil, err
	}
	if err := validateActiveCallModel(userID, p.model); err != nil {
		return nil, err
	}
	if err := validateDemoUser(userID, p.db); err != nil {
		return nil, err
	}
	request := &v1.StartOutgoingCallRequest{UserId: userID, Provider: req.GetProvider(), Target: req.GetTarget()}
	response, err := backend.StartOutgoingCall(ctx, request)
	if err != nil {
		return nil, err
	}
	p.calls.Store(response.GetCallId(), req.GetProvider())
	return response, nil
}

func validateDemoUser(userID uint32, checker DB) error {
	if checker == nil {
		return status.Error(codes.Unavailable, "demo user checker is not configured")
	}
	isDemo, err := checker.CheckDemo(userID)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to check demo user: %v", err)
	}
	if isDemo {
		return status.Error(codes.FailedPrecondition, "voice calls are unavailable for demo users")
	}
	return nil
}

func validateActiveCallModel(userID uint32, provider ActiveModelProvider) error {
	if provider == nil {
		return status.Error(codes.Unavailable, "active model validator is not configured")
	}
	model, err := provider.GetActiveUserModel(userID)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get active model: %v", err)
	}
	if model == nil {
		return status.Error(codes.FailedPrecondition, "active model is not configured")
	}
	if !model.Realtime {
		return status.Error(codes.FailedPrecondition, "active model does not support realtime")
	}
	if model.RealtimeVAD == nil || model.RealtimeVAD.InitialGreeting == nil || !*model.RealtimeVAD.InitialGreeting {
		return status.Error(codes.FailedPrecondition, "initial greeting mode is required")
	}
	if model.RealtimeVAD.Greeting == nil || strings.TrimSpace(*model.RealtimeVAD.Greeting) == "" {
		return status.Error(codes.FailedPrecondition, "greeting phrase is required")
	}
	return nil
}

func (p *CallsProxy) SubscribeCallEvents(req *v1.SubscribeCallEventsRequest, stream v1.Calls_SubscribeCallEventsServer) error {
	userID, err := userIDFromContext(stream.Context())
	if err != nil {
		return err
	}
	value, ok := p.calls.Load(req.GetCallId())
	if !ok {
		return status.Error(codes.NotFound, "call not found")
	}
	provider, ok := value.(v1.CallProvider)
	if !ok {
		return status.Error(codes.Internal, "invalid call provider state")
	}
	backend, err := p.backend(provider)
	if err != nil {
		return err
	}
	backendStream, err := backend.SubscribeCallEvents(stream.Context(), &v1.SubscribeCallEventsRequest{UserId: userID, CallId: req.GetCallId(), AfterSequence: req.GetAfterSequence()})
	if err != nil {
		return err
	}
	for {
		event, err := backendStream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(event); err != nil {
			return err
		}
		if event.GetType() == v1.CallEventType_CALL_ENDED {
			p.calls.Delete(req.GetCallId())
			return nil
		}
	}
}

func (p *CallsProxy) HangupCall(ctx context.Context, req *v1.HangupCallRequest) (*v1.HangupCallResponse, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	value, ok := p.calls.Load(req.GetCallId())
	if !ok {
		return nil, status.Error(codes.NotFound, "call not found")
	}
	provider, ok := value.(v1.CallProvider)
	if !ok {
		return nil, status.Error(codes.Internal, "invalid call provider state")
	}
	backend, err := p.backend(provider)
	if err != nil {
		return nil, err
	}
	return backend.HangupCall(ctx, &v1.HangupCallRequest{UserId: userID, CallId: req.GetCallId(), Reason: req.GetReason()})
}

func (p *CallsProxy) backend(provider v1.CallProvider) (v1.CallsClient, error) {
	switch provider {
	case v1.CallProvider_CALL_PROVIDER_WHATSAPP:
		if p.whatsapp == nil {
			return nil, status.Error(codes.Unavailable, "WhatsApp calls backend is unavailable")
		}
		return p.whatsapp, nil
	case v1.CallProvider_CALL_PROVIDER_TELEGRAM:
		if p.telegram == nil {
			return nil, status.Error(codes.Unavailable, "Telegram calls backend is unavailable")
		}
		return p.telegram, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported call provider")
	}
}
