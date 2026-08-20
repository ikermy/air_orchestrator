package storage

import "testing"

func TestValidateKey(t *testing.T) {
	for _, key := range []string{"", " ", "\t"} {
		if err := validateKey(key); err == nil {
			t.Errorf("validateKey(%q) error = nil", key)
		}
	}
	if err := validateKey("user/file.txt"); err != nil {
		t.Fatalf("validateKey() error = %v", err)
	}
}
