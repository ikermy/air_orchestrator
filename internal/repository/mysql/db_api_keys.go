package db

import (
	"context"
	"fmt"
	"time"

	"github.com/ikermy/air_common/pkg/crypto"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// ============================================================================
// API Keys Encryption — шифрование существующих ключей при создании MasterKey
// ============================================================================

// EncryptUserAPIKeysWSS шифрует все plaintext API-ключи пользователя его MasterKey ($mk$).
// Вызывается один раз из CreateMasterKeyWSS сразу после генерации MasterKey.
// masterKey — расшифрованный MasterKey из Exam.masterKeyCache.
func (d *DB) EncryptUserAPIKeysWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 30*time.Second)
	defer cancel()

	query := `SELECT Provider, ApiKey FROM user_api_keys WHERE UserId = ?`
	rows, err := d.Conn().QueryContext(ctx, query, userId)
	if err != nil {
		return fmt.Errorf("failed to get API keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type apiKeyRecord struct {
		provider string
		apiKey   string
	}

	var keys []apiKeyRecord
	for rows.Next() {
		var provider, apiKey string
		if err := rows.Scan(&provider, &apiKey); err != nil {
			return fmt.Errorf("failed to scan API key: %w", err)
		}
		// Пропускаем уже зашифрованные ($mk$ и $app$)
		if crypto.IsEncryptedWithMasterKey(apiKey) || crypto.IsEncryptedWithAppKey(apiKey) {
			continue
		}
		keys = append(keys, apiKeyRecord{provider: provider, apiKey: apiKey})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate API keys: %w", err)
	}

	if len(keys) == 0 {
		if progressCallback != nil {
			progressCallback("NO_KEYS_TO_ENCRYPT")
		}
		return nil
	}

	encrypted := 0
	for _, k := range keys {
		if progressCallback != nil {
			progressCallback(fmt.Sprintf("ENCRYPTING_KEY:%s", k.provider))
		}
		encryptedKey, err := crypto.EncryptFieldWithMasterKey(masterKey, k.apiKey)
		if err != nil {
			logger.Error("Failed to encrypt API key for provider %s: %v", k.provider, err, userId)
			continue
		}
		updateQuery := `UPDATE user_api_keys SET ApiKey = ? WHERE UserId = ? AND Provider = ?`
		if _, err := d.Conn().ExecContext(ctx, updateQuery, encryptedKey, userId, k.provider); err != nil {
			logger.Error("Failed to save encrypted API key for provider %s: %v", k.provider, err, userId)
			continue
		}
		encrypted++
	}

	if progressCallback != nil && encrypted > 0 {
		progressCallback(fmt.Sprintf("ENCRYPTED_COUNT:%d", encrypted))
	}
	logger.Info("Encrypted %d API keys for user with MasterKey ($mk$)", encrypted, userId)
	return nil
}
