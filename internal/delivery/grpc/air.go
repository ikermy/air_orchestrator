package grpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"air_orchestrator/internal/db"
	"air_orchestrator/internal/repository/mysql"

	airv1 "github.com/ikermy/air-common/v2/gen/go/air/v1"
	"github.com/ikermy/air-common/v2/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// AirDeps — зависимости data-plane: БД для auth/session и model router.
type AirDeps struct {
	DB    *db.DB
	Model *model.Router
}

// airServer implements air.v1.AirDataServer.
type airServer struct {
	airv1.UnimplementedAirDataServer
	db       *db.DB
	model    *model.Router
	provider ExamProvider
	hub      *relayHub
	modelSem *deviceSemaphore
	sttSem   *deviceSemaphore
}

func newAirServer(deps *AirDeps, provider ExamProvider) *airServer {
	return &airServer{
		db:       deps.DB,
		model:    deps.Model,
		provider: provider,
		hub:      newRelayHub(),
		modelSem: newDeviceSemaphore(envLimit("AIR_MODEL_MAX_STREAMS_PER_DEVICE", 2)),
		sttSem:   newDeviceSemaphore(envLimit("AIR_STT_MAX_STREAMS_PER_DEVICE", 1)),
	}
}

func hashDeviceToken(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func (a *airServer) authDevice(ctx context.Context) (*mysql.AirDevice, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "device") {
		return nil, status.Error(codes.Unauthenticated, "device token required")
	}
	d, err := a.db.AirDeviceByTokenHash(ctx, hashDeviceToken(parts[1]))
	if err != nil {
		return nil, status.Error(codes.Internal, "device lookup failed")
	}
	if d == nil || d.Status != "active" {
		return nil, status.Error(codes.Unauthenticated, "invalid device token")
	}
	return d, nil
}

func (a *airServer) authBearer(ctx context.Context) (uint64, error) {
	if a.provider == nil {
		return 0, status.Error(codes.Unavailable, "access token verifier is not configured")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0, status.Error(codes.Unauthenticated, "missing metadata")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return 0, status.Error(codes.Unauthenticated, "missing authorization")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return 0, status.Error(codes.Unauthenticated, "bearer token required")
	}
	userID, _, err := a.provider.VerifyAccessToken(parts[1])
	if err != nil || userID == 0 {
		return 0, status.Error(codes.Unauthenticated, "invalid bearer token")
	}
	return userID, nil
}

// ModelStream — стриминг ответа модели через orchestrator.
func (a *airServer) ModelStream(stream airv1.AirData_ModelStreamServer) error {
	dev, err := a.authDevice(stream.Context())
	if err != nil {
		return err
	}
	if !a.modelSem.acquire(dev.DeviceID) {
		return status.Error(codes.ResourceExhausted, "too many model streams for device")
	}
	defer a.modelSem.release(dev.DeviceID)
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first frame must be model start")
	}
	if a.model == nil {
		return status.Error(codes.Unavailable, "model gateway is not configured")
	}

	if err := a.ensureDialog(dev.UserID, start.DialogId); err != nil {
		return stream.Send(&airv1.ModelEvent{
			RequestId: start.RequestId,
			Event: &airv1.ModelEvent_Error{Error: &airv1.ModelError{
				Code: "dialog_init", Message: err.Error(),
			}},
		})
	}

	files, err := convertFiles(start.Files)
	if err != nil {
		return stream.Send(&airv1.ModelEvent{
			RequestId: start.RequestId,
			Event: &airv1.ModelEvent_Error{Error: &airv1.ModelError{
				Code: "bad_files", Message: err.Error(),
			}},
		})
	}

	var full strings.Builder
	err = a.model.RequestStreaming(dev.UserID, start.DialogId, start.Text, func(delta string, done bool) error {
		if done {
			return nil
		}
		full.WriteString(delta)
		return stream.Send(&airv1.ModelEvent{
			RequestId: start.RequestId,
			Event:     &airv1.ModelEvent_Delta{Delta: &airv1.ModelDelta{Text: delta}},
		})
	}, files...)
	if err != nil {
		return stream.Send(&airv1.ModelEvent{
			RequestId: start.RequestId,
			Event: &airv1.ModelEvent_Error{Error: &airv1.ModelError{
				Code: "provider_error", Message: err.Error(),
			}},
		})
	}

	return stream.Send(&airv1.ModelEvent{
		RequestId: start.RequestId,
		Event:     &airv1.ModelEvent_Done{Done: &airv1.ModelDone{Message: full.String()}},
	})
}

// RealtimeStt — MVP: принимает аудио и возвращает финальный транскрипт батчем.
// Реалтайм-частичные фразы добавим позже, контракт уже bidi.
func (a *airServer) RealtimeStt(stream airv1.AirData_RealtimeSttServer) error {
	dev, err := a.authDevice(stream.Context())
	if err != nil {
		return err
	}
	if !a.sttSem.acquire(dev.DeviceID) {
		return status.Error(codes.ResourceExhausted, "too many stt streams for device")
	}
	defer a.sttSem.release(dev.DeviceID)
	var (
		start *airv1.SttStart
		audio []byte
	)
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch p := req.Payload.(type) {
		case *airv1.SttRequest_Start:
			start = p.Start
		case *airv1.SttRequest_Audio:
			if len(audio) < 32<<20 {
				audio = append(audio, p.Audio.Pcm16...)
			}
		case *airv1.SttRequest_Stop:
			goto transcribe
		}
	}

transcribe:
	if start == nil {
		return status.Error(codes.InvalidArgument, "missing stt start")
	}
	if a.model == nil {
		return status.Error(codes.Unavailable, "stt gateway is not configured")
	}
	text, err := a.model.TranscribeAudio(dev.UserID, audio, "audio.wav")
	if err != nil {
		return stream.Send(&airv1.SttEvent{
			SessionId: start.SessionId,
			Event:     &airv1.SttEvent_Error{Error: &airv1.SttError{Code: "stt_error", Message: err.Error()}},
		})
	}
	return stream.Send(&airv1.SttEvent{
		SessionId: start.SessionId,
		Event:     &airv1.SttEvent_Final{Final: &airv1.SttFinal{Text: text}},
	})
}

func (a *airServer) ensureDialog(userID, dialogID uint64) error {
	rec, err := a.db.GetActiveModel(userID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("активная модель не найдена для uid=%d", userID)
	}
	_, err = a.model.GetOrSetRespGPT(model.Assistant{
		UserID:   userID,
		Provider: rec.Provider,
		AssistId: rec.AssistId,
	}, dialogID, dialogID, rec.AssistId)
	return err
}

func convertFiles(in []*airv1.File) ([]model.FileUpload, error) {
	out := make([]model.FileUpload, 0, len(in))
	for _, f := range in {
		if f == nil {
			continue
		}
		data := f.Data
		if len(data) == 0 && f.Name != "" {
			if raw, err := os.ReadFile(f.Name); err == nil {
				data = raw
			}
		}
		if len(data) == 0 {
			continue
		}
		out = append(out, model.FileUpload{
			Name:     f.Name,
			MimeType: f.MimeType,
			Content:  bytes.NewReader(data),
		})
	}
	return out, nil
}

// ── Relay ────────────────────────────────────────────────────────────────────

type relayPeer struct {
	deviceID  string
	sessionID string
	send      chan *airv1.RelayFrame
	done      chan struct{}
	seq       uint64
	mu        sync.Mutex
}

func newRelayPeer(deviceID string) *relayPeer {
	return &relayPeer{
		deviceID: deviceID,
		send:     make(chan *airv1.RelayFrame, 256),
		done:     make(chan struct{}),
	}
}

func (p *relayPeer) close() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}

func (p *relayPeer) push(frame *airv1.RelayFrame) {
	if frame == nil {
		return
	}
	p.mu.Lock()
	p.seq++
	frame.Sequence = p.seq
	p.mu.Unlock()
	select {
	case p.send <- frame:
	default:
		// Без истории: медленный получатель теряет промежуточные дельты.
	}
}

func (p *relayPeer) setSession(id string) {
	p.mu.Lock()
	p.sessionID = id
	p.mu.Unlock()
}

func (p *relayPeer) getSession() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionID
}

type relayHub struct {
	mu      sync.RWMutex
	agents  map[string]*relayPeer // deviceID
	viewers map[string]*relayPeer // sessionID
}

func newRelayHub() *relayHub {
	return &relayHub{
		agents:  make(map[string]*relayPeer),
		viewers: make(map[string]*relayPeer),
	}
}

func (h *relayHub) addAgent(deviceID string, p *relayPeer) {
	h.mu.Lock()
	h.agents[deviceID] = p
	h.mu.Unlock()
}

func (h *relayHub) removeAgent(deviceID string, p *relayPeer) {
	h.mu.Lock()
	if h.agents[deviceID] == p {
		delete(h.agents, deviceID)
	}
	h.mu.Unlock()
	p.close()
}

func (h *relayHub) addViewer(sessionID, deviceID string, p *relayPeer) {
	h.mu.Lock()
	h.viewers[sessionID] = p
	if a, ok := h.agents[deviceID]; ok {
		a.setSession(sessionID)
	}
	h.mu.Unlock()
}

func (h *relayHub) removeViewer(sessionID string, p *relayPeer) {
	h.mu.Lock()
	if h.viewers[sessionID] == p {
		delete(h.viewers, sessionID)
	}
	h.mu.Unlock()
	p.close()
}

func (h *relayHub) agent(deviceID string) *relayPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[deviceID]
}

func (h *relayHub) viewer(sessionID string) *relayPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.viewers[sessionID]
}

// sessionForDevice возвращает активную viewer-сессию устройства, если есть.
func (h *relayHub) sessionForDevice(deviceID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sid, v := range h.viewers {
		if v.deviceID == deviceID {
			return sid
		}
	}
	return ""
}

func (h *relayHub) bindSession(deviceID, sessionID string) {
	h.mu.Lock()
	if a, ok := h.agents[deviceID]; ok {
		a.setSession(sessionID)
	}
	h.mu.Unlock()
}

func (h *relayHub) requestResync(deviceID string) {
	if a := h.agent(deviceID); a != nil {
		payload, _ := json.Marshal(map[string]string{"command": "resync"})
		a.push(&airv1.RelayFrame{Type: "command", Payload: payload})
	}
}

func (a *airServer) writePeer(stream interface {
	Send(*airv1.RelayFrame) error
}, p *relayPeer) {
	for {
		select {
		case <-p.done:
			return
		case f := <-p.send:
			if err := stream.Send(f); err != nil {
				p.close()
				return
			}
		}
	}
}

// Relay связывает агент и viewer по session_id.
func (a *airServer) Relay(stream airv1.AirData_RelayServer) error {
	ctx := stream.Context()
	first, err := stream.Recv()
	if err != nil {
		return err
	}

	switch {
	case first.Hello != nil || strings.EqualFold(first.Type, "hello"):
		dev, err := a.authDevice(ctx)
		if err != nil {
			return err
		}
		if hello := first.Hello; hello != nil && hello.DeviceId != "" && hello.DeviceId != dev.DeviceID {
			return status.Error(codes.PermissionDenied, "device id mismatch")
		}
		peer := newRelayPeer(dev.DeviceID)
		a.hub.addAgent(dev.DeviceID, peer)
		defer a.hub.removeAgent(dev.DeviceID, peer)
		go a.writePeer(stream, peer)

		// Если viewer уже подключён — попросим агент прислать snapshot.
		if sid := a.hub.sessionForDevice(dev.DeviceID); sid != "" {
			peer.setSession(sid)
			a.hub.requestResync(dev.DeviceID)
		}

		for {
			f, err := stream.Recv()
			if err != nil {
				return nil
			}
			sessionID := peer.getSession()
			if sessionID == "" {
				s, err := a.db.AirActiveSessionByDevice(ctx, dev.DeviceID)
				if err == nil && s != nil {
					sessionID = s.SessionID
					peer.setSession(sessionID)
					a.hub.bindSession(dev.DeviceID, sessionID)
				}
			}
			if sessionID == "" {
				continue // нет viewer — без истории
			}
			f.SessionId = sessionID
			if v := a.hub.viewer(sessionID); v != nil {
				v.push(f)
			}
		}

	default:
		return status.Error(codes.InvalidArgument, "first frame must be hello")
	}
}

// Watch — server-streaming для web-viewer (gRPC-Web).
func (a *airServer) Watch(req *airv1.WatchRequest, stream airv1.AirData_WatchServer) error {
	userID, err := a.authBearer(stream.Context())
	if err != nil {
		return err
	}
	sessionID := req.GetSessionId()
	if sessionID == "" {
		return status.Error(codes.InvalidArgument, "session_id required")
	}
	s, err := a.db.AirSessionByID(stream.Context(), sessionID)
	if err != nil {
		return status.Error(codes.Internal, "session lookup failed")
	}
	if s == nil || s.UserID != userID || s.Status != "active" {
		return status.Error(codes.PermissionDenied, "session is not active")
	}

	peer := newRelayPeer(s.DeviceID)
	peer.setSession(sessionID)
	a.hub.addViewer(sessionID, s.DeviceID, peer)
	defer a.hub.removeViewer(sessionID, peer)
	go a.writePeer(stream, peer)

	a.hub.bindSession(s.DeviceID, sessionID)
	a.hub.requestResync(s.DeviceID)

	select {
	case <-stream.Context().Done():
		return nil
	case <-peer.done:
		return nil
	}
}
