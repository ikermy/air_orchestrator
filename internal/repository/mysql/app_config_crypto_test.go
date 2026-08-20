package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppConfigEncryptDecryptRoundTrip(t *testing.T) {
	masterKey := "test-master-key"
	plaintext := "super-secret-value"

	enc, err := encryptAppConfigValue(masterKey, plaintext)
	if err != nil {
		t.Fatalf("encryptAppConfigValue() error = %v", err)
	}
	if !isEncryptedAppConfigValue(enc) {
		t.Fatalf("expected encrypted prefix, got %q", enc)
	}
	if enc == plaintext {
		t.Fatalf("ciphertext must differ from plaintext")
	}

	dec, err := decryptAppConfigValue(masterKey, enc)
	if err != nil {
		t.Fatalf("decryptAppConfigValue() error = %v", err)
	}
	if dec != plaintext {
		t.Fatalf("decrypt mismatch: got %q want %q", dec, plaintext)
	}
}

func TestIsSensitiveAppConfigKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"auth.session", true},
		{"smtp.pass", true},
		{"google_oauth.client_secret", true},
		{"tg.token", true},
		{"feature.flag", false},
		{"web.port", false},
	}

	for _, tt := range tests {
		if got := isSensitiveAppConfigKey(tt.key); got != tt.want {
			t.Fatalf("isSensitiveAppConfigKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestRekeyRoundTripBetweenTwoMasterKeys(t *testing.T) {
	oldKey := "old-master-key"
	newKey := "new-master-key"
	plaintext := "rotate-me"

	oldEnc, err := encryptAppConfigValue(oldKey, plaintext)
	if err != nil {
		t.Fatalf("encrypt old: %v", err)
	}

	dec, err := decryptAppConfigValue(oldKey, oldEnc)
	if err != nil {
		t.Fatalf("decrypt old: %v", err)
	}
	if dec != plaintext {
		t.Fatalf("decrypt old mismatch: got %q want %q", dec, plaintext)
	}

	newEnc, err := encryptAppConfigValue(newKey, dec)
	if err != nil {
		t.Fatalf("encrypt new: %v", err)
	}
	if oldEnc == newEnc {
		t.Fatalf("ciphertext should change after rekey")
	}

	newDec, err := decryptAppConfigValue(newKey, newEnc)
	if err != nil {
		t.Fatalf("decrypt new: %v", err)
	}
	if newDec != plaintext {
		t.Fatalf("decrypt new mismatch: got %q want %q", newDec, plaintext)
	}
}

func TestIsAppConfigRekeyMode(t *testing.T) {
	orig := os.Getenv("APP_CONFIG_REKEY")
	defer os.Setenv("APP_CONFIG_REKEY", orig)

	if err := os.Setenv("APP_CONFIG_REKEY", "true"); err != nil {
		t.Fatalf("Setenv true: %v", err)
	}
	if !IsAppConfigRekeyMode() {
		t.Fatalf("expected APP_CONFIG_REKEY=true to enable rekey mode")
	}

	if err := os.Setenv("APP_CONFIG_REKEY", "false"); err != nil {
		t.Fatalf("Setenv false: %v", err)
	}
	if IsAppConfigRekeyMode() {
		t.Fatalf("expected APP_CONFIG_REKEY=false to disable rekey mode")
	}
}

func TestIsAppConfigRekeyDryRun(t *testing.T) {
	orig := os.Getenv("APP_CONFIG_REKEY_DRY_RUN")
	defer os.Setenv("APP_CONFIG_REKEY_DRY_RUN", orig)

	if err := os.Setenv("APP_CONFIG_REKEY_DRY_RUN", "true"); err != nil {
		t.Fatalf("Setenv true: %v", err)
	}
	if !IsAppConfigRekeyDryRun() {
		t.Fatalf("expected APP_CONFIG_REKEY_DRY_RUN=true to enable dry-run mode")
	}

	if err := os.Setenv("APP_CONFIG_REKEY_DRY_RUN", "false"); err != nil {
		t.Fatalf("Setenv false: %v", err)
	}
	if IsAppConfigRekeyDryRun() {
		t.Fatalf("expected APP_CONFIG_REKEY_DRY_RUN=false to disable dry-run mode")
	}
}

func TestLoadCurrentAppMasterKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "app_master_key.txt")
	if err := os.WriteFile(secretFile, []byte("file-master-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origFile := os.Getenv("APP_MASTER_KEY_FILE")
	origEnv := os.Getenv("APP_MASTER_KEY")
	defer os.Setenv("APP_MASTER_KEY_FILE", origFile)
	defer os.Setenv("APP_MASTER_KEY", origEnv)

	_ = os.Unsetenv("APP_MASTER_KEY")
	if err := os.Setenv("APP_MASTER_KEY_FILE", secretFile); err != nil {
		t.Fatalf("Setenv APP_MASTER_KEY_FILE: %v", err)
	}

	got, ok, err := loadCurrentAppMasterKey()
	if err != nil {
		t.Fatalf("loadCurrentAppMasterKey: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got != "file-master-key" {
		t.Fatalf("got %q want %q", got, "file-master-key")
	}
}

func TestLoadNewAppMasterKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "new_app_master_key.txt")
	if err := os.WriteFile(secretFile, []byte("new-file-master-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origFile := os.Getenv("NEW_APP_MASTER_KEY_FILE")
	origEnv := os.Getenv("NEW_APP_MASTER_KEY")
	defer os.Setenv("NEW_APP_MASTER_KEY_FILE", origFile)
	defer os.Setenv("NEW_APP_MASTER_KEY", origEnv)

	_ = os.Unsetenv("NEW_APP_MASTER_KEY")
	if err := os.Setenv("NEW_APP_MASTER_KEY_FILE", secretFile); err != nil {
		t.Fatalf("Setenv NEW_APP_MASTER_KEY_FILE: %v", err)
	}

	got, ok, err := loadNewAppMasterKey()
	if err != nil {
		t.Fatalf("loadNewAppMasterKey: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got != "new-file-master-key" {
		t.Fatalf("got %q want %q", got, "new-file-master-key")
	}
}

func TestValidateAppConfigRekeyConfig(t *testing.T) {
	origMaster := os.Getenv("APP_MASTER_KEY")
	origNew := os.Getenv("NEW_APP_MASTER_KEY")
	origMasterFile := os.Getenv("APP_MASTER_KEY_FILE")
	origNewFile := os.Getenv("NEW_APP_MASTER_KEY_FILE")
	defer os.Setenv("APP_MASTER_KEY", origMaster)
	defer os.Setenv("NEW_APP_MASTER_KEY", origNew)
	defer os.Setenv("APP_MASTER_KEY_FILE", origMasterFile)
	defer os.Setenv("NEW_APP_MASTER_KEY_FILE", origNewFile)

	_ = os.Unsetenv("APP_MASTER_KEY_FILE")
	_ = os.Unsetenv("NEW_APP_MASTER_KEY_FILE")

	if err := os.Setenv("APP_MASTER_KEY", "old"); err != nil {
		t.Fatalf("Setenv APP_MASTER_KEY: %v", err)
	}
	if err := os.Setenv("NEW_APP_MASTER_KEY", "new"); err != nil {
		t.Fatalf("Setenv NEW_APP_MASTER_KEY: %v", err)
	}
	if err := ValidateAppConfigRekeyConfig(); err != nil {
		t.Fatalf("ValidateAppConfigRekeyConfig valid case: %v", err)
	}

	if err := os.Setenv("NEW_APP_MASTER_KEY", ""); err != nil {
		t.Fatalf("clear NEW_APP_MASTER_KEY: %v", err)
	}
	if err := ValidateAppConfigRekeyConfig(); err == nil {
		t.Fatalf("expected error when NEW_APP_MASTER_KEY is empty")
	}

	if err := os.Setenv("NEW_APP_MASTER_KEY", "old"); err != nil {
		t.Fatalf("set same key: %v", err)
	}
	if err := ValidateAppConfigRekeyConfig(); err == nil {
		t.Fatalf("expected error when APP_MASTER_KEY and NEW_APP_MASTER_KEY are equal")
	}
}
