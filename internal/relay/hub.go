// Package relay implements in-memory rooms between an air_phantom agent and
// a web-viewer. It is shared by the gRPC data-plane and the HTTP control-plane
// so that revoking a device closes its active streams immediately.
package relay

import (
	"encoding/json"
	"sync"

	airv1 "github.com/ikermy/air-common/v2/gen/go/air/v1"
)

// Peer — одно gRPC-соединение (агент или viewer).
type Peer struct {
	DeviceID  string
	UserID    uint64
	SessionID string

	send chan *airv1.RelayFrame
	done chan struct{}
	seq  uint64
	mu   sync.Mutex
}

func NewPeer(deviceID string, userID uint64) *Peer {
	return &Peer{
		DeviceID: deviceID,
		UserID:   userID,
		send:     make(chan *airv1.RelayFrame, 256),
		done:     make(chan struct{}),
	}
}

func (p *Peer) Out() <-chan *airv1.RelayFrame { return p.send }
func (p *Peer) Done() <-chan struct{}         { return p.done }

func (p *Peer) Close() {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}

func (p *Peer) Push(frame *airv1.RelayFrame) {
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

func (p *Peer) SetSession(id string) {
	p.mu.Lock()
	p.SessionID = id
	p.mu.Unlock()
}

func (p *Peer) GetSession() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.SessionID
}

// Hub — реестр агентов и viewer'ов.
type Hub struct {
	mu      sync.RWMutex
	agents  map[string]*Peer // deviceID
	viewers map[string]*Peer // sessionID
}

func NewHub() *Hub {
	return &Hub{
		agents:  make(map[string]*Peer),
		viewers: make(map[string]*Peer),
	}
}

var defaultHub = NewHub()

// Default возвращает общий hub процесса.
func Default() *Hub { return defaultHub }

func (h *Hub) AddAgent(deviceID string, p *Peer) {
	h.mu.Lock()
	h.agents[deviceID] = p
	h.mu.Unlock()
}

func (h *Hub) RemoveAgent(deviceID string, p *Peer) {
	h.mu.Lock()
	if h.agents[deviceID] == p {
		delete(h.agents, deviceID)
	}
	h.mu.Unlock()
	p.Close()
}

func (h *Hub) AddViewer(sessionID, deviceID string, p *Peer) {
	h.mu.Lock()
	h.viewers[sessionID] = p
	if a, ok := h.agents[deviceID]; ok {
		a.SetSession(sessionID)
	}
	h.mu.Unlock()
}

func (h *Hub) RemoveViewer(sessionID string, p *Peer) {
	h.mu.Lock()
	if h.viewers[sessionID] == p {
		delete(h.viewers, sessionID)
	}
	h.mu.Unlock()
	p.Close()
}

func (h *Hub) Agent(deviceID string) *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[deviceID]
}

func (h *Hub) Viewer(sessionID string) *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.viewers[sessionID]
}

// SessionForDevice возвращает активную viewer-сессию устройства.
func (h *Hub) SessionForDevice(deviceID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sid, v := range h.viewers {
		if v.DeviceID == deviceID {
			return sid
		}
	}
	return ""
}

func (h *Hub) BindSession(deviceID, sessionID string) {
	h.mu.Lock()
	if a, ok := h.agents[deviceID]; ok {
		a.SetSession(sessionID)
	}
	h.mu.Unlock()
}

// RequestResync просит агент прислать актуальный snapshot.
func (h *Hub) RequestResync(deviceID string) {
	if a := h.Agent(deviceID); a != nil {
		payload, _ := json.Marshal(map[string]string{"command": "resync"})
		a.Push(&airv1.RelayFrame{Type: "command", Payload: payload})
	}
}

// DisconnectDevice закрывает все стримы устройства (revoke/forget).
func (h *Hub) DisconnectDevice(deviceID string) {
	h.mu.Lock()
	var peers []*Peer
	if a, ok := h.agents[deviceID]; ok {
		peers = append(peers, a)
		delete(h.agents, deviceID)
	}
	for sid, v := range h.viewers {
		if v.DeviceID == deviceID {
			peers = append(peers, v)
			delete(h.viewers, sid)
		}
	}
	h.mu.Unlock()
	for _, p := range peers {
		p.Close()
	}
}

// DisconnectUserExcept вытесняет стримы других устройств пользователя.
func (h *Hub) DisconnectUserExcept(userID uint64, keepDeviceID string) {
	h.mu.Lock()
	var peers []*Peer
	for id, a := range h.agents {
		if a.UserID == userID && id != keepDeviceID {
			peers = append(peers, a)
			delete(h.agents, id)
		}
	}
	for sid, v := range h.viewers {
		if v.UserID == userID && v.DeviceID != keepDeviceID {
			peers = append(peers, v)
			delete(h.viewers, sid)
		}
	}
	h.mu.Unlock()
	for _, p := range peers {
		p.Close()
	}
}
