package db

import (
	"air_orchestrator/internal/domain/state"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const appConfigEncPrefix = "enc:v1:"

var sensitiveAppConfigPrefixes = []string{
	"auth.",
	"smtp.",
	"google_oauth.",
	"tg.",
	"oper.",
	"svc.",  // сервисные ключи межсервисной авторизации
	"widg.", // Ed25519 долгоживущий ключ виджетов
}

type appConfigItem struct {
	key   string
	value string
}

type appConfigRekeyItem struct {
	key       string
	plaintext string
	newValue  string
}

type AppConfigRekeyResult struct {
	Count  int
	Keys   []string
	DryRun bool
}

// EncryptAppConfigSensitiveValues шифрует уже существующие plaintext-значения
// чувствительных ключей в app_config. Если APP_MASTER_KEY не задан — это no-op.
//
// Метод полезен для мягкой миграции: старые открытые значения продолжают читаться,
// а при наличии master key могут быть переведены в encrypted storage одной операцией.
func (d *DB) EncryptAppConfigSensitiveValues() error {
	masterKey, ok, err := loadCurrentAppMasterKey()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	ctx, cancel := context.WithTimeout(d.Context(), 30*time.Second)
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx, "SELECT `key`, `value` FROM app_config")
	if err != nil {
		return fmt.Errorf("EncryptAppConfigSensitiveValues query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var updates []appConfigItem
	for rows.Next() {
		var it appConfigItem
		if err := rows.Scan(&it.key, &it.value); err != nil {
			return fmt.Errorf("EncryptAppConfigSensitiveValues scan: %w", err)
		}
		if !isSensitiveAppConfigKey(it.key) || it.value == "" || isEncryptedAppConfigValue(it.value) {
			continue
		}
		enc, err := encryptAppConfigValue(masterKey, it.value)
		if err != nil {
			return fmt.Errorf("EncryptAppConfigSensitiveValues encrypt[%s]: %w", it.key, err)
		}
		updates = append(updates, appConfigItem{key: it.key, value: enc})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("EncryptAppConfigSensitiveValues rows: %w", err)
	}
	if len(updates) == 0 {
		return nil
	}

	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("EncryptAppConfigSensitiveValues begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, it := range updates {
		if _, err := tx.ExecContext(ctx,
			"UPDATE app_config SET `value` = ? WHERE `key` = ?",
			it.value, it.key); err != nil {
			return fmt.Errorf("EncryptAppConfigSensitiveValues update[%s]: %w", it.key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("EncryptAppConfigSensitiveValues commit: %w", err)
	}
	return nil
}

// ValidateAppConfigRekeyConfig валидирует окружение для режима перекодирования.
func ValidateAppConfigRekeyConfig() error {
	oldKey, okOld, err := loadCurrentAppMasterKey()
	if err != nil {
		return err
	}
	newKey, okNew, err := loadNewAppMasterKey()
	if err != nil {
		return err
	}
	if !okOld {
		return fmt.Errorf("APP_CONFIG_REKEY=true, но APP_MASTER_KEY не задан")
	}
	if !okNew {
		return fmt.Errorf("APP_CONFIG_REKEY=true, но NEW_APP_MASTER_KEY не задан")
	}
	if strings.TrimSpace(oldKey) == strings.TrimSpace(newKey) {
		return fmt.Errorf("APP_MASTER_KEY и NEW_APP_MASTER_KEY совпадают")
	}
	return nil
}

// RekeyAppConfigSensitiveValues выполняет one-shot перекодирование чувствительных
// ключей app_config со старого APP_MASTER_KEY на NEW_APP_MASTER_KEY.
//
// Гарантии безопасности:
//   - используется одна транзакция;
//   - строки блокируются через SELECT ... FOR UPDATE;
//   - после UPDATE выполняется повторное чтение и расшифровка новым ключом;
//   - при любой ошибке выполняется rollback всех изменений.
//
// Возвращает количество реально перекодированных чувствительных ключей и их имена.
func (d *DB) RekeyAppConfigSensitiveValues() (*AppConfigRekeyResult, error) {
	if err := ValidateAppConfigRekeyConfig(); err != nil {
		return nil, err
	}
	oldKey, _, err := loadCurrentAppMasterKey()
	if err != nil {
		return nil, err
	}
	newKey, _, err := loadNewAppMasterKey()
	if err != nil {
		return nil, err
	}
	result := &AppConfigRekeyResult{DryRun: IsAppConfigRekeyDryRun()}

	ctx, cancel := context.WithTimeout(d.Context(), 60*time.Second)
	defer cancel()

	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("RekeyAppConfigSensitiveValues begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, "SELECT `key`, `value` FROM app_config FOR UPDATE")
	if err != nil {
		return nil, fmt.Errorf("RekeyAppConfigSensitiveValues query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var updates []appConfigRekeyItem
	for rows.Next() {
		var it appConfigItem
		if err := rows.Scan(&it.key, &it.value); err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues scan: %w", err)
		}
		if !isSensitiveAppConfigKey(it.key) || it.value == "" {
			continue
		}

		plaintext := it.value
		if isEncryptedAppConfigValue(it.value) {
			plaintext, err = decryptAppConfigValue(oldKey, it.value)
			if err != nil {
				return nil, fmt.Errorf("RekeyAppConfigSensitiveValues decrypt[%s]: %w", it.key, err)
			}
		}

		reencoded, err := encryptAppConfigValue(newKey, plaintext)
		if err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues encrypt[%s]: %w", it.key, err)
		}
		if _, err := decryptAppConfigValue(newKey, reencoded); err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues verify[%s]: %w", it.key, err)
		}
		updates = append(updates, appConfigRekeyItem{key: it.key, plaintext: plaintext, newValue: reencoded})
		result.Keys = append(result.Keys, it.key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("RekeyAppConfigSensitiveValues rows: %w", err)
	}
	result.Count = len(updates)
	if len(updates) == 0 {
		if result.DryRun {
			if err := tx.Rollback(); err != nil {
				return nil, fmt.Errorf("RekeyAppConfigSensitiveValues empty dry-run rollback: %w", err)
			}
			committed = true
			return result, nil
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues empty commit: %w", err)
		}
		committed = true
		return result, nil
	}

	if result.DryRun {
		if err := tx.Rollback(); err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues dry-run rollback: %w", err)
		}
		committed = true
		return result, nil
	}

	for _, it := range updates {
		if _, err := tx.ExecContext(ctx,
			"UPDATE app_config SET `value` = ? WHERE `key` = ?",
			it.newValue, it.key); err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues update[%s]: %w", it.key, err)
		}

		var storedValue string
		if err := tx.QueryRowContext(ctx,
			"SELECT `value` FROM app_config WHERE `key` = ?",
			it.key).Scan(&storedValue); err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues readback[%s]: %w", it.key, err)
		}
		if !isEncryptedAppConfigValue(storedValue) {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues readback[%s]: значение не зашифровано", it.key)
		}
		decoded, err := decryptAppConfigValue(newKey, storedValue)
		if err != nil {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues decrypt-new[%s]: %w", it.key, err)
		}
		if decoded != it.plaintext {
			return nil, fmt.Errorf("RekeyAppConfigSensitiveValues mismatch[%s]: verify failed", it.key)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("RekeyAppConfigSensitiveValues commit: %w", err)
	}
	committed = true
	return result, nil
}

func (d *DB) prepareAppConfigValueForStorage(key, value string) (string, error) {
	if value == "" || !isSensitiveAppConfigKey(key) {
		return value, nil
	}
	if isEncryptedAppConfigValue(value) {
		return value, nil
	}
	masterKey, ok, err := loadCurrentAppMasterKey()
	if err != nil {
		return "", err
	}
	if !ok {
		return value, nil
	}
	return encryptAppConfigValue(masterKey, value)
}

func (d *DB) decodeAppConfigValue(key, value string) (string, error) {
	if value == "" || !isEncryptedAppConfigValue(value) {
		return value, nil
	}
	masterKey, ok, err := loadCurrentAppMasterKey()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("значение %q зашифровано, но APP_MASTER_KEY не задан", key)
	}
	return decryptAppConfigValue(masterKey, value)
}

func isSensitiveAppConfigKey(key string) bool {
	for _, prefix := range sensitiveAppConfigPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func isEncryptedAppConfigValue(value string) bool {
	return strings.HasPrefix(value, appConfigEncPrefix)
}

func loadCurrentAppMasterKey() (string, bool, error) {
	key := string(state.MasterKey)
	if key == "" {
		return "", false, nil
	}
	return key, true, nil
}

func loadNewAppMasterKey() (string, bool, error) {
	if filePath := strings.TrimSpace(os.Getenv("NEW_APP_MASTER_KEY_FILE")); filePath != "" {
		value, err := readSecretFile(filePath)
		if err != nil {
			return "", false, fmt.Errorf("NEW_APP_MASTER_KEY_FILE: %w", err)
		}
		if value != "" {
			return value, true, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("NEW_APP_MASTER_KEY")); v != "" {
		return v, true, nil
	}
	return "", false, nil
}

func IsAppConfigRekeyMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_CONFIG_REKEY")), "true")
}

func IsAppConfigRekeyDryRun() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_CONFIG_REKEY_DRY_RUN")), "true")
}

func encryptAppConfigValue(masterKey, plaintext string) (string, error) {
	block, err := aes.NewCipher(deriveAppConfigAESKey(masterKey))
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return appConfigEncPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptAppConfigValue(masterKey, encoded string) (string, error) {
	rawB64 := strings.TrimPrefix(encoded, appConfigEncPrefix)
	ciphertext, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	block, err := aes.NewCipher(deriveAppConfigAESKey(masterKey))
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, payload := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return "", fmt.Errorf("gcm.Open: %w", err)
	}
	return string(plaintext), nil
}

func deriveAppConfigAESKey(masterKey string) []byte {
	sum := sha256.Sum256([]byte(masterKey))
	return sum[:]
}

func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// compile-time quiet imports for sql package when this file is built together with db methods.
var _ = sql.ErrNoRows
