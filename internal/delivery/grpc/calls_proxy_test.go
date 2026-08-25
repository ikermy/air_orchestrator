package grpc

import (
	"air_orchestrator/internal/delivery/grpc/v1"
	"context"
	"net"
	"testing"

	"github.com/ikermy/air-common/pkg/comdom"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type mockCallsBackend struct {
	v1.UnimplementedCallsServer
	events  []*v1.CallEvent
	started *v1.StartOutgoingCallRequest
	hangup  *v1.HangupCallRequest
}

type mockActiveModelProvider struct {
	model *comdom.UniversalModelData
}

type mockDemoChecker struct {
	isDemo bool
	err    error
}

func (m mockDemoChecker) CheckDemo(uint32) (bool, error) { return m.isDemo, m.err }

func (m mockActiveModelProvider) GetActiveUserModel(uint32) (*comdom.UniversalModelData, error) {
	return m.model, nil
}

func validCallModel() mockActiveModelProvider {
	initialGreeting := true
	greeting := "Здравствуйте"
	return mockActiveModelProvider{model: &comdom.UniversalModelData{
		Realtime: true,
		RealtimeVAD: &comdom.RealtimeVAD{
			InitialGreeting: &initialGreeting,
			Greeting:        &greeting,
		},
	}}
}

func (m *mockCallsBackend) StartOutgoingCall(_ context.Context, req *v1.StartOutgoingCallRequest) (*v1.StartOutgoingCallResponse, error) {
	m.started = req
	return &v1.StartOutgoingCallResponse{CallId: "call-1", Status: "starting"}, nil
}

func (m *mockCallsBackend) SubscribeCallEvents(req *v1.SubscribeCallEventsRequest, stream v1.Calls_SubscribeCallEventsServer) error {
	for _, event := range m.events {
		if event.Sequence > req.GetAfterSequence() {
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *mockCallsBackend) HangupCall(_ context.Context, req *v1.HangupCallRequest) (*v1.HangupCallResponse, error) {
	m.hangup = req
	return &v1.HangupCallResponse{CallId: req.GetCallId(), Status: "hangup_requested"}, nil
}

func newMockClient(t *testing.T, backend v1.CallsServer) v1.CallsClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	v1.RegisterCallsServer(srv, backend)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return v1.NewCallsClient(conn)
}

func TestCallsProxyForwardsProviderAndUser(t *testing.T) {
	backend := &mockCallsBackend{}
	proxy := NewCallsProxy(newMockClient(t, backend), nil, mockDemoChecker{})
	proxy.SetModelProvider(validCallModel())
	ctx := WithUserID(context.Background(), 23)
	response, err := proxy.StartOutgoingCall(ctx, &v1.StartOutgoingCallRequest{
		Provider: v1.CallProvider_CALL_PROVIDER_WHATSAPP,
		Target:   "79222913731",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetCallId() != "call-1" || backend.started.GetUserId() != 23 {
		t.Fatalf("request was not forwarded correctly: %+v", backend.started)
	}
}

func TestCallsProxyReconnectsWithAfterSequence(t *testing.T) {
	backend := &mockCallsBackend{events: []*v1.CallEvent{
		{CallId: "call-1", Sequence: 1, Type: v1.CallEventType_CALL_STARTED},
		{CallId: "call-1", Sequence: 2, Type: v1.CallEventType_RESPONSE_TEXT_DELTA, Delta: "hello"},
		{CallId: "call-1", Sequence: 3, Type: v1.CallEventType_CALL_ENDED},
	}}
	proxy := NewCallsProxy(newMockClient(t, backend), nil, mockDemoChecker{})
	proxy.SetModelProvider(validCallModel())
	ctx := WithUserID(context.Background(), 23)
	if _, err := proxy.StartOutgoingCall(ctx, &v1.StartOutgoingCallRequest{Provider: v1.CallProvider_CALL_PROVIDER_WHATSAPP, Target: "target"}); err != nil {
		t.Fatal(err)
	}
	stream := &proxyStream{ctx: ctx}
	if err := proxy.SubscribeCallEvents(&v1.SubscribeCallEventsRequest{CallId: "call-1", AfterSequence: 1}, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.events) != 2 || stream.events[0].GetSequence() != 2 || stream.events[1].GetSequence() != 3 {
		t.Fatalf("unexpected replay: %+v", stream.events)
	}
}

func TestValidateActiveCallModelRequiresRealtimeGreeting(t *testing.T) {
	initialGreeting := true
	greeting := "hello"
	cases := []struct {
		name  string
		model *comdom.UniversalModelData
	}{
		{name: "missing model"},
		{name: "realtime disabled", model: &comdom.UniversalModelData{}},
		{name: "missing realtime config", model: &comdom.UniversalModelData{Realtime: true}},
		{name: "greeting disabled", model: &comdom.UniversalModelData{Realtime: true, RealtimeVAD: &comdom.RealtimeVAD{InitialGreeting: new(bool), Greeting: &greeting}}},
		{name: "missing greeting", model: &comdom.UniversalModelData{Realtime: true, RealtimeVAD: &comdom.RealtimeVAD{InitialGreeting: &initialGreeting}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateActiveCallModel(23, mockActiveModelProvider{model: tc.model}); err == nil {
				t.Fatal("expected model validation error")
			}
		})
	}
	if err := validateActiveCallModel(23, validCallModel()); err != nil {
		t.Fatalf("valid model rejected: %v", err)
	}
}

func TestValidateDemoUser(t *testing.T) {
	if err := validateDemoUser(23, mockDemoChecker{isDemo: true}); err == nil {
		t.Fatal("expected demo user to be rejected")
	}
	if err := validateDemoUser(23, mockDemoChecker{}); err != nil {
		t.Fatalf("regular user rejected: %v", err)
	}
}

type proxyStream struct {
	ctx    context.Context
	events []*v1.CallEvent
}

func (s *proxyStream) Send(event *v1.CallEvent) error {
	s.events = append(s.events, event)
	return nil
}
func (s *proxyStream) SetHeader(_ metadata.MD) error  { return nil }
func (s *proxyStream) SendHeader(_ metadata.MD) error { return nil }
func (s *proxyStream) SetTrailer(_ metadata.MD)       {}
func (s *proxyStream) Context() context.Context       { return s.ctx }
func (s *proxyStream) SendMsg(any) error              { return nil }
func (s *proxyStream) RecvMsg(any) error              { return nil }
