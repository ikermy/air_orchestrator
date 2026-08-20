package storage

import (
	"context"
	"testing"
)

func TestProbeSTSCapabilityRejectsIncompleteConfig(t *testing.T) {
	if ok, err := ProbeSTSCapability(context.Background(), "", "access", "secret", "bucket", "us-east-1"); err == nil || ok {
		t.Fatalf("expected incomplete configuration error, got ok=%v err=%v", ok, err)
	}
}
