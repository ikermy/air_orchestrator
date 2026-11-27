package grpc

import (
	"testing"

	airv1 "github.com/ikermy/air-common/v2/gen/go/air/v1"
)

func TestRelayHubSessionForDevice(t *testing.T) {
	h := newRelayHub()
	v := newRelayPeer("dev-1")
	h.addViewer("sess-1", "dev-1", v)
	defer h.removeViewer("sess-1", v)

	if got := h.sessionForDevice("dev-1"); got != "sess-1" {
		t.Fatalf("sessionForDevice = %q, want sess-1", got)
	}
	if got := h.sessionForDevice("dev-2"); got != "" {
		t.Fatalf("sessionForDevice(dev-2) = %q, want empty", got)
	}
}

func TestRelayPeerSequenceAndOverflow(t *testing.T) {
	p := newRelayPeer("dev")
	defer p.close()

	p.push(&airv1.RelayFrame{Type: "status"})
	p.push(&airv1.RelayFrame{Type: "answer_delta"})

	if got := (<-p.send).GetSequence(); got != 1 {
		t.Fatalf("first sequence = %d, want 1", got)
	}
	if got := (<-p.send).GetSequence(); got != 2 {
		t.Fatalf("second sequence = %d, want 2", got)
	}
}

// Компиляционная проверка, что airServer реализует сервис (без запуска).
var _ airv1.AirDataServer = (*airServer)(nil)
