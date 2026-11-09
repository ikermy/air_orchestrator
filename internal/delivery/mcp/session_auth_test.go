package mcp

import (
	"testing"
	"time"

	"github.com/ikermy/air-common/v2/pkg/comdom"
	"github.com/ikermy/air-common/v2/pkg/mcpsession"
)

func TestAuthenticate_LegacyAllowedByDefault(t *testing.T) {
	h := &Handler{}
	uid, provider, err := h.authenticate("42:3")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if uid != 42 || provider != comdom.ProviderType(3) {
		t.Fatalf("unexpected values: uid=%d provider=%d", uid, provider)
	}
}

func TestAuthenticate_RequiresSigned(t *testing.T) {
	secret := "test-secret"
	h := &Handler{sessionSecret: secret, requireSigned: true}

	if _, _, err := h.authenticate("42:3"); err == nil {
		t.Fatal("legacy header must be rejected when signed sessions are required")
	}

	header := mcpsession.Sign(secret, 42, comdom.ProviderType(3), time.Now())
	uid, provider, err := h.authenticate(header)
	if err != nil {
		t.Fatalf("authenticate signed: %v", err)
	}
	if uid != 42 || provider != comdom.ProviderType(3) {
		t.Fatalf("unexpected values: uid=%d provider=%d", uid, provider)
	}
}

func TestAuthenticate_RejectsTamperedSigned(t *testing.T) {
	secret := "test-secret"
	h := &Handler{sessionSecret: secret, requireSigned: true}
	header := mcpsession.Sign(secret, 42, comdom.ProviderType(3), time.Now())
	last := header[len(header)-1]
	replacement := byte('0')
	if last == '0' {
		replacement = '1'
	}
	header = header[:len(header)-1] + string(replacement)
	if _, _, err := h.authenticate(header); err == nil {
		t.Fatal("tampered signature must be rejected")
	}
}

func TestAuthenticate_RequireSignedWithoutSecret(t *testing.T) {
	h := &Handler{requireSigned: true}
	if _, _, err := h.authenticate("42:3"); err == nil {
		t.Fatal("must reject when secret is not configured")
	}
}

func TestCheckServiceKey(t *testing.T) {
	h := &Handler{}
	if !h.checkServiceKey("anything") {
		t.Fatal("service key check must be disabled when no key is configured")
	}

	h.SetServiceAuth("secret")
	if h.checkServiceKey("wrong") {
		t.Fatal("wrong service key must be rejected")
	}
	if !h.checkServiceKey("secret") {
		t.Fatal("valid service key must be accepted")
	}
}
