package relay

import (
	"testing"
	"time"

	airv1 "github.com/ikermy/air-common/v2/gen/go/air/v1"
)

func TestSessionForDevice(t *testing.T) {
	h := NewHub()
	v := NewPeer("dev-1", 7)
	h.AddViewer("sess-1", "dev-1", v)
	defer h.RemoveViewer("sess-1", v)

	if got := h.SessionForDevice("dev-1"); got != "sess-1" {
		t.Fatalf("SessionForDevice = %q, want sess-1", got)
	}
	if got := h.SessionForDevice("dev-2"); got != "" {
		t.Fatalf("SessionForDevice(dev-2) = %q, want empty", got)
	}
}

func TestPeerSequenceAndOverflow(t *testing.T) {
	p := NewPeer("dev", 1)
	defer p.Close()

	p.Push(&airv1.RelayFrame{Type: "status"})
	p.Push(&airv1.RelayFrame{Type: "answer_delta"})

	if got := (<-p.Out()).GetSequence(); got != 1 {
		t.Fatalf("first sequence = %d, want 1", got)
	}
	if got := (<-p.Out()).GetSequence(); got != 2 {
		t.Fatalf("second sequence = %d, want 2", got)
	}
}

func TestDisconnectDeviceClosesAgentAndViewers(t *testing.T) {
	h := NewHub()
	a := NewPeer("dev-1", 7)
	v := NewPeer("dev-1", 7)
	h.AddAgent("dev-1", a)
	h.AddViewer("sess-1", "dev-1", v)

	h.DisconnectDevice("dev-1")

	select {
	case <-a.Done():
	case <-time.After(time.Second):
		t.Fatal("agent peer was not closed")
	}
	select {
	case <-v.Done():
	case <-time.After(time.Second):
		t.Fatal("viewer peer was not closed")
	}
	if h.Agent("dev-1") != nil || h.Viewer("sess-1") != nil {
		t.Fatal("peers must be removed from the hub")
	}
}

func TestDisconnectUserExcept(t *testing.T) {
	h := NewHub()
	oldDevice := NewPeer("old", 42)
	keep := NewPeer("new", 42)
	other := NewPeer("other", 43)
	h.AddAgent("old", oldDevice)
	h.AddAgent("new", keep)
	h.AddAgent("other", other)

	h.DisconnectUserExcept(42, "new")

	select {
	case <-oldDevice.Done():
	case <-time.After(time.Second):
		t.Fatal("old device peer was not closed")
	}
	select {
	case <-keep.Done():
		t.Fatal("kept device must stay connected")
	default:
	}
	select {
	case <-other.Done():
		t.Fatal("other user must stay connected")
	default:
	}
}
