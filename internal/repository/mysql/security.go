package db

// security.go — методы безопасности: lazy migration bcrypt+AES, GetAuthData.
// Вынесены в отдельный файл, чтобы избежать проблем с кодировкой в db.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ikermy/air-common/pkg/crypto"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// GetAuthData возвращает данные для авторизации, ища пользователя по plaintext Email
// (старые пользователи) или по EmailHash (мигрированные/новые пользователи).
// isLegacy=true означает что пользователь ещё не мигрирован (EmailHash IS NULL).
func (d *DB) GetAuthData(email, emailHMAC string) (storedHash string, userId uint32, confirmed, disabled, isLegacy bool, err error) {
	if email == "" && emailHMAC == "" {
		return "", 0, false, false, false, fmt.Errorf("получены пустые значения")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	query := `
	SELECT ua.SHA, u.Id, ua.Confirmed, ua.Disabled, (ua.EmailHash IS NULL) AS IsLegacy
	FROM user_auth ua
	JOIN users u ON ua.UserId = u.Id
	WHERE ua.Email = ? OR ua.EmailHash = ?
	LIMIT 1`

	var legacyInt int
	scanErr := d.Conn().QueryRowContext(ctx, query, email, emailHMAC).
		Scan(&storedHash, &userId, &confirmed, &disabled, &legacyInt)
	if scanErr != nil {
		switch {
		case errors.Is(scanErr, context.DeadlineExceeded):
			return "", 0, false, false, false, fmt.Errorf("тайм-аут (%d с) при получении данных авторизации: %w", mode.GetSQLTimeToCancel(), scanErr)
		case errors.Is(scanErr, context.Canceled):
			return "", 0, false, false, false, fmt.Errorf("операция отменена: %w", scanErr)
		case errors.Is(scanErr, sql.ErrNoRows):
			return "", 0, false, false, false, nil // пользователь не найден — не ошибка
		default:
			return "", 0, false, false, false, fmt.Errorf("ошибка получения данных авторизации: %w", scanErr)
		}
	}

	isLegacy = legacyInt == 1
	return storedHash, userId, confirmed, disabled, isLegacy, nil
}

// MigrateUserSecurity обновляет SHA и Email пользователя до более безопасного формата.
// Обновляет только строки где EmailHash IS NULL (пользователь ещё не мигрирован).
// После успешного обновления запускает проверку завершения общей миграции.
func (d *DB) MigrateUserSecurity(userId uint32, newHash, encEmail, emailHMAC string) error {
	if userId == 0 || newHash == "" || encEmail == "" || emailHMAC == "" {
		return fmt.Errorf("получены некорректные данные для миграции пользователя %d", userId)
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	result, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET SHA = ?, Email = ?, EmailHash = ? WHERE UserId = ? AND EmailHash IS NULL",
		newHash, encEmail, emailHMAC, userId)
	if err != nil {
		return fmt.Errorf("ошибка миграции безопасности пользователя %d: %w", userId, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка чтения результата миграции: %w", err)
	}

	if rows > 0 {
		logger.Info("DB: пользователь успешно мигрирован на bcrypt+AES", userId)
		go d.checkAndFinalizeMigration()
	}

	return nil
}

// checkAndFinalizeMigration проверяет, все ли пользователи прошли lazy migration.
// Если COUNT(*) WHERE EmailHash IS NULL == 0 — выполняет финальный ALTER TABLE.
func (d *DB) checkAndFinalizeMigration() {
	d.migrationMu.Lock()
	defer d.migrationMu.Unlock()

	if d.migrationDone {
		return
	}

	ctx, cancel := context.WithTimeout(d.Context(), 60*time.Second)
	defer cancel()

	var count int
	if err := d.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_auth WHERE EmailHash IS NULL").Scan(&count); err != nil {
		logger.Error("DB: ошибка проверки незамигрированных пользователей: %v", err)
		return
	}

	if count > 0 {
		logger.Info("DB: lazy migration: осталось незамигрированных пользователей: %d", count)
		return
	}

	// Все пользователи мигрированы — финализируем схему
	if _, err := d.Conn().ExecContext(ctx,
		"ALTER TABLE user_auth MODIFY `EmailHash` VARCHAR(64) NOT NULL COLLATE 'utf8mb4_general_ci'"); err != nil {
		logger.Error("DB: ошибка финализации миграции (NOT NULL): %v", err)
		return
	}

	d.migrationDone = true
	logger.Info("DB: lazy migration завершена — EmailHash переведён в NOT NULL")
}

// ============================================================================
// TOTP
// ============================================================================
// SaveTOTPSecret сохраняет зашифрованный TOTP secret.
// TOTPSecret IS NOT NULL означает, что TOTP включён для пользователя.
func (d *DB) SaveTOTPSecret(ctx context.Context, userId uint32, encSecret string) error {
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET TOTPSecret = ? WHERE UserId = ?",
		encSecret, userId)
	if err != nil {
		return fmt.Errorf("ошибка сохранения TOTP secret: %w", err)
	}
	return nil
}

// GetTOTPData возвращает зашифрованный TOTP secret.
// enabled = true когда TOTPSecret IS NOT NULL (тогда TOTP активен).
func (d *DB) GetTOTPData(ctx context.Context, userId uint32) (encSecret string, enabled bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	var secret sql.NullString
	scanErr := d.Conn().QueryRowContext(ctx,
		"SELECT TOTPSecret FROM user_auth WHERE UserId = ?", userId).
		Scan(&secret)
	if scanErr != nil {
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			return "", false, nil
		case errors.Is(scanErr, context.DeadlineExceeded):
			return "", false, fmt.Errorf("тайм-аут при получении TOTP данных: %w", scanErr)
		default:
			return "", false, fmt.Errorf("ошибка получения TOTP данных: %w", scanErr)
		}
	}
	if !secret.Valid || secret.String == "" {
		return "", false, nil
	}
	return secret.String, true, nil
}

// ClearTOTPSecret обнуляет TOTPSecret — отключает TOTP для пользователя.
func (d *DB) ClearTOTPSecret(ctx context.Context, userId uint32) error {
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET TOTPSecret = NULL WHERE UserId = ?", userId)
	if err != nil {
		return fmt.Errorf("ошибка удаления TOTP secret: %w", err)
	}
	return nil
}

// ============================================================================
// MasterKey
// ============================================================================

// SaveMasterKey сохраняет зашифрованный MasterKey и соль обёртки (PBKDF2).
// Используется при первичной генерации и при смене/сбросе пароля.
// GetPasswordHash возвращает сохранённый хеш пароля (bcrypt или legacy SHA3) по userId.
// Используется для верификации пароля без знания email (например, в CreateMasterKey).
func (d *DB) GetPasswordHash(userId uint32) (string, error) {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var storedHash string
	err := d.Conn().QueryRowContext(ctx,
		"SELECT SHA FROM user_auth WHERE UserId = ?", userId).Scan(&storedHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("пользователь %d не найден", userId)
		}
		return "", fmt.Errorf("ошибка получения хеша пароля: %w", err)
	}
	return storedHash, nil
}

func (d *DB) SaveMasterKey(userId uint32, encMK, wrapSalt string) error {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET MasterKey = ?, WrapSalt = ? WHERE UserId = ?",
		encMK, wrapSalt, userId)
	if err != nil {
		return fmt.Errorf("ошибка сохранения MasterKey: %w", err)
	}
	return nil
}

// GetMasterKeyData возвращает зашифрованный MasterKey и соль.
// hasMK=false если MasterKey ещё не создан (колонки NULL).
func (d *DB) GetMasterKeyData(userId uint32) (encMK, wrapSalt string, hasMK bool, err error) {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var mk, ws sql.NullString
	scanErr := d.Conn().QueryRowContext(ctx,
		"SELECT MasterKey, WrapSalt FROM user_auth WHERE UserId = ?", userId).
		Scan(&mk, &ws)
	if scanErr != nil {
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			return "", "", false, nil
		default:
			return "", "", false, fmt.Errorf("ошибка получения MasterKey данных: %w", scanErr)
		}
	}
	if !mk.Valid || !ws.Valid || mk.String == "" || ws.String == "" {
		return "", "", false, nil
	}
	return mk.String, ws.String, true, nil
}

// ClearMasterKey обнуляет MasterKey и WrapSalt пользователя.
// Вызывается при сбросе пароля без raw MasterKey — после удаления всех зашифрованных данных.
func (d *DB) ClearMasterKey(userId uint32) error {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET MasterKey = NULL, WrapSalt = NULL WHERE UserId = ?", userId)
	if err != nil {
		return fmt.Errorf("ошибка очистки MasterKey: %w", err)
	}
	return nil
}

// DeleteEncryptedUserData удаляет все зашифрованные данные пользователя из БД.
// Вызывается при сбросе пароля без raw MasterKey (ключ утрачен — данные недоступны).
func (d *DB) DeleteEncryptedUserData(userId uint32) error {
	if userId == 0 {
		return fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции удаления зашифрованных данных: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	queries := []string{
		`DELETE FROM user_api_keys WHERE UserId = ?`,
		`UPDATE channels SET TgBot = NULL, Widget = NULL, TgUserBot = NULL, Whats = NULL, Insta = NULL, Avito = NULL,
			TgBot_enabled = 0, Widget_enabled = 0, TgUserBot_enabled = 0, Whats_enabled = 0, Insta_enabled = 0, Avito_enabled = 0 WHERE UserId = ?`,
		"UPDATE dialogs SET Data = NULL WHERE `User` = ?",
		`DELETE FROM google_oauth_tokens WHERE user_id = ?`,
		`DELETE FROM vector_embeddings WHERE user_id = ?`,
		`DELETE FROM crm_configs WHERE user_id = ?`,
		`DELETE FROM crm_oauth_states WHERE user_id = ?`,
		`UPDATE user_storage_config SET access_key_ciphertext = NULL, secret_key_ciphertext = NULL WHERE user_id = ?`,
		`UPDATE service_tgbots SET AuthData = NULL WHERE UserId = ?`,
		`UPDATE service_wabots SET AuthData = NULL WHERE UserId = ?`,
	}

	for _, query := range queries {
		if _, err = tx.ExecContext(ctx, query, userId); err != nil {
			return fmt.Errorf("ошибка удаления зашифрованных данных пользователя: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации удаления зашифрованных данных: %w", err)
	}
	return nil
}

func (d *DB) decryptChannelField(userId uint32, s sql.NullString) json.RawMessage {
	if !s.Valid || s.String == "" {
		return json.RawMessage(`{}`)
	}
	val := s.String
	if crypto.IsEncryptedWithMasterKey(val) {
		if d.MasterKeyResolver == nil {
			logger.Warn("GetChannelsData: MasterKey resolver not configured", userId)
			return json.RawMessage(`{}`)
		}
		mk, ok := d.MasterKeyResolver(userId)
		if !ok {
			logger.Warn("GetChannelsData: MasterKey not in cache (login required)", userId)
			return json.RawMessage(`{}`)
		}
		plain, err := crypto.DecryptFieldWithMasterKey(mk, val)
		if err != nil {
			logger.Error("GetChannelsData: failed to decrypt channel field: %v", userId, err)
			return json.RawMessage(`{}`)
		}
		val = plain
	}

	if json.Valid([]byte(val)) {
		return json.RawMessage(val)
	}
	return json.RawMessage(`{}`)
}

// EncryptChannelsWSS шифрует существующие plaintext данные каналов MasterKey'ом ($mk$).
// Вызывается из CreateMasterKeyWSS после генерации MasterKey.
func (d *DB) EncryptChannelsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 30*time.Second)
	defer cancel()

	type colDef struct {
		name string
		col  string
		val  sql.NullString
	}
	cols := []colDef{
		{name: "tgbot", col: "TgBot"},
		{name: "widget", col: "Widget"},
		{name: "tgubot", col: "TgUserBot"},
		{name: "whatsbot", col: "Whats"},
		{name: "insta", col: "Insta"},
		{name: "avito", col: "Avito"},
	}

	err := d.Conn().QueryRowContext(ctx,
		`SELECT TgBot, Widget, TgUserBot, Whats, Insta, Avito FROM channels WHERE UserId = ?`, userId).
		Scan(&cols[0].val, &cols[1].val, &cols[2].val, &cols[3].val, &cols[4].val, &cols[5].val)
	if errors.Is(err, sql.ErrNoRows) {
		if progressCallback != nil {
			progressCallback("NO_CHANNELS_TO_ENCRYPT")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("EncryptChannelsWSS: failed to read channels: %w", err)
	}

	encrypted := 0
	for i := range cols {
		c := &cols[i]
		if !c.val.Valid || c.val.String == "" || crypto.IsEncryptedWithMasterKey(c.val.String) {
			continue
		}
		if progressCallback != nil {
			progressCallback(fmt.Sprintf("ENCRYPTING_CHANNEL:%s", c.name))
		}
		encVal, encErr := crypto.EncryptFieldWithMasterKey(masterKey, c.val.String)
		if encErr != nil {
			logger.Error("EncryptChannelsWSS: failed to encrypt %s: %v", c.col, encErr, userId)
			continue
		}
		if _, updErr := d.Conn().ExecContext(ctx,
			fmt.Sprintf("UPDATE channels SET %s = ? WHERE UserId = ?", c.col), encVal, userId); updErr != nil {
			logger.Error("EncryptChannelsWSS: failed to save %s: %v", c.col, updErr, userId)
			continue
		}
		encrypted++
	}

	if progressCallback != nil {
		if encrypted == 0 {
			progressCallback("NO_CHANNELS_TO_ENCRYPT")
		} else {
			progressCallback(fmt.Sprintf("ENCRYPTED_CHANNELS_COUNT:%d", encrypted))
		}
	}
	logger.Info("EncryptChannelsWSS: encrypted %d channel fields for user", encrypted, userId)
	return nil
}

// EncryptDialogsWSS шифрует все plaintext Data во всех диалогах пользователя.
func (d *DB) EncryptDialogsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 60*time.Second)
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT Id, Data FROM dialogs WHERE `User` = ?", userId)
	if err != nil {
		return fmt.Errorf("EncryptDialogsWSS: query: %w", err)
	}
	defer rows.Close()

	type record struct {
		id   uint64
		data string
	}
	var toEncrypt []record
	for rows.Next() {
		var id uint64
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("EncryptDialogsWSS: scan: %w", err)
		}
		if !raw.Valid || raw.String == "" || crypto.IsEncryptedWithMasterKey(raw.String) {
			continue // пусто или уже $mk$
		}
		toEncrypt = append(toEncrypt, record{id: id, data: raw.String})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("EncryptDialogsWSS: rows: %w", err)
	}

	if len(toEncrypt) == 0 {
		if progressCallback != nil {
			progressCallback("NO_DIALOGS_TO_ENCRYPT")
		}
		return nil
	}

	if progressCallback != nil {
		progressCallback(fmt.Sprintf("ENCRYPTING_DIALOGS:%d", len(toEncrypt)))
	}

	encrypted := 0
	for _, r := range toEncrypt {
		encVal, err := crypto.EncryptFieldWithMasterKey(masterKey, r.data)
		if err != nil {
			logger.Error("EncryptDialogsWSS: encrypt dialog %d: %v", r.id, err, userId)
			continue
		}
		if _, err := d.Conn().ExecContext(ctx,
			"UPDATE dialogs SET Data = ? WHERE Id = ?", encVal, r.id); err != nil {
			logger.Error("EncryptDialogsWSS: update dialog %d: %v", r.id, err, userId)
			continue
		}
		encrypted++
	}

	if progressCallback != nil {
		progressCallback(fmt.Sprintf("ENCRYPTED_DIALOGS_COUNT:%d", encrypted))
	}
	logger.Info("EncryptDialogsWSS: encrypted %d dialogs for user", encrypted, userId)
	return nil
}

// EncryptGoogleTokenWSS шифрует access_token и refresh_token Google OAuth токенов MasterKey'ом.
func (d *DB) EncryptGoogleTokenWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 30*time.Second)
	defer cancel()

	var accessToken, refreshToken sql.NullString
	err := d.Conn().QueryRowContext(ctx,
		"SELECT access_token, refresh_token FROM google_oauth_tokens WHERE user_id = ?", userId).
		Scan(&accessToken, &refreshToken)
	if errors.Is(err, sql.ErrNoRows) {
		if progressCallback != nil {
			progressCallback("NO_GOOGLE_TOKEN_TO_ENCRYPT")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("EncryptGoogleTokenWSS: read: %w", err)
	}

	newAccess := accessToken.String
	newRefresh := refreshToken.String
	changed := false

	if accessToken.Valid && accessToken.String != "" && !crypto.IsEncryptedWithMasterKey(accessToken.String) {
		if progressCallback != nil {
			progressCallback("ENCRYPTING_GOOGLE_TOKEN")
		}
		if enc, err := crypto.EncryptFieldWithMasterKey(masterKey, accessToken.String); err == nil {
			newAccess = enc
			changed = true
		}
	}
	if refreshToken.Valid && refreshToken.String != "" && !crypto.IsEncryptedWithMasterKey(refreshToken.String) {
		if enc, err := crypto.EncryptFieldWithMasterKey(masterKey, refreshToken.String); err == nil {
			newRefresh = enc
			changed = true
		}
	}

	if changed {
		_, err = d.Conn().ExecContext(ctx,
			"UPDATE google_oauth_tokens SET access_token = ?, refresh_token = ? WHERE user_id = ?",
			newAccess, newRefresh, userId)
		if err != nil {
			return fmt.Errorf("EncryptGoogleTokenWSS: update: %w", err)
		}
		if progressCallback != nil {
			progressCallback("ENCRYPTED_GOOGLE_TOKEN")
		}
	} else {
		if progressCallback != nil {
			progressCallback("NO_GOOGLE_TOKEN_TO_ENCRYPT")
		}
	}

	logger.Info("EncryptGoogleTokenWSS: Google OAuth tokens encrypted for user", userId)
	return nil
}

func (d *DB) EncryptVectorEmbeddingsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 120*time.Second) // документов может быть много
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT id, doc_name, content FROM vector_embeddings WHERE user_id = ?", userId)
	if err != nil {
		return fmt.Errorf("EncryptVectorEmbeddingsWSS: query: %w", err)
	}
	defer rows.Close()

	type record struct {
		id      uint64
		docName string
		content string
	}
	var toEncrypt []record
	for rows.Next() {
		var id uint64
		var docName, content string
		rows.Scan(&id, &docName, &content)
		// Пропускаем уже зашифрованные
		if crypto.IsEncryptedWithMasterKey(docName) && crypto.IsEncryptedWithMasterKey(content) {
			continue
		}
		toEncrypt = append(toEncrypt, record{id, docName, content})
	}

	if len(toEncrypt) == 0 {
		progressCallback("NO_VECTORS_TO_ENCRYPT")
		return nil
	}

	progressCallback(fmt.Sprintf("ENCRYPTING_VECTORS:%d", len(toEncrypt)))

	encrypted := 0
	for _, r := range toEncrypt {
		encName, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.docName)
		encContent, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.content)
		d.Conn().ExecContext(ctx,
			"UPDATE vector_embeddings SET doc_name = ?, content = ? WHERE id = ?",
			encName, encContent, r.id)
		encrypted++
	}

	progressCallback(fmt.Sprintf("ENCRYPTED_VECTORS_COUNT:%d", encrypted))
	return nil
}

func (d *DB) EncryptCRMConfigsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 120*time.Second)
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT id, name, subdomain, credentials, options, channels FROM crm_configs WHERE user_id = ?", userId)
	if err != nil {
		return fmt.Errorf("EncryptCRMConfigsWSS: query: %w", err)
	}
	defer rows.Close()

	type record struct {
		id          uint64
		name        string
		subdomain   sql.NullString
		credentials string
		options     string
		channels    sql.NullString
	}
	var toEncrypt []record
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.id, &r.name, &r.subdomain, &r.credentials, &r.options, &r.channels); err != nil {
			return fmt.Errorf("EncryptCRMConfigsWSS: scan: %w", err)
		}

		// Пропускаем уже зашифрованные
		if crypto.IsEncryptedWithMasterKey(r.name) &&
			(!r.subdomain.Valid || crypto.IsEncryptedWithMasterKey(r.subdomain.String)) &&
			crypto.IsEncryptedWithMasterKey(r.credentials) &&
			crypto.IsEncryptedWithMasterKey(r.options) &&
			(!r.channels.Valid || crypto.IsEncryptedWithMasterKey(r.channels.String)) {
			continue
		}

		toEncrypt = append(toEncrypt, r)
	}

	if len(toEncrypt) == 0 {
		progressCallback("NO_CRM_CONFIGS_TO_ENCRYPT")
		return nil
	}

	progressCallback(fmt.Sprintf("ENCRYPTING_CRM_CONFIGS:%d", len(toEncrypt)))

	encrypted := 0
	for _, r := range toEncrypt {
		encName, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.name)

		var encSubdomain interface{} = nil
		if r.subdomain.Valid {
			encSubdomain, _ = crypto.EncryptFieldWithMasterKey(masterKey, r.subdomain.String)
		}

		encCredentials, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.credentials)
		encOptions, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.options)

		var encChannels interface{} = nil
		if r.channels.Valid {
			encChannels, _ = crypto.EncryptFieldWithMasterKey(masterKey, r.channels.String)
		}

		_, err = d.Conn().ExecContext(ctx,
			"UPDATE crm_configs SET name = ?, subdomain = ?, credentials = ?, options = ?, channels = ? WHERE id = ?",
			encName, encSubdomain, encCredentials, encOptions, encChannels, r.id)
		if err != nil {
			return fmt.Errorf("EncryptCRMConfigsWSS: update: %w", err)
		}
		encrypted++
	}

	progressCallback(fmt.Sprintf("ENCRYPTED_CRM_CONFIGS_COUNT:%d", encrypted))
	return nil
}

func (d *DB) EncryptCRMOAuthStatesWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 120*time.Second)
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT state, client_id, client_secret, redirect_url, subdomain, crm_type FROM crm_oauth_states WHERE user_id = ?", userId)
	if err != nil {
		return fmt.Errorf("EncryptCRMOAuthStatesWSS: query: %w", err)
	}
	defer rows.Close()

	type record struct {
		state        string
		clientID     string
		clientSecret string
		redirectURL  string
		subdomain    string
		crmType      string
	}
	var toEncrypt []record
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.state, &r.clientID, &r.clientSecret, &r.redirectURL, &r.subdomain, &r.crmType); err != nil {
			return fmt.Errorf("EncryptCRMOAuthStatesWSS: scan: %w", err)
		}

		// Пропускаем уже зашифрованные
		if crypto.IsEncryptedWithMasterKey(r.clientID) &&
			crypto.IsEncryptedWithMasterKey(r.clientSecret) &&
			crypto.IsEncryptedWithMasterKey(r.redirectURL) &&
			crypto.IsEncryptedWithMasterKey(r.subdomain) {
			continue
		}

		toEncrypt = append(toEncrypt, r)
	}

	if len(toEncrypt) == 0 {
		progressCallback("NO_OAUTH_STATES_TO_ENCRYPT")
		return nil
	}

	progressCallback(fmt.Sprintf("ENCRYPTING_OAUTH_STATES:%d", len(toEncrypt)))

	encrypted := 0
	for _, r := range toEncrypt {
		encClientID, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.clientID)
		encClientSecret, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.clientSecret)
		encRedirectURL, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.redirectURL)
		encSubdomain, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.subdomain)

		_, err = d.Conn().ExecContext(ctx,
			"UPDATE crm_oauth_states SET client_id = ?, client_secret = ?, redirect_url = ?, subdomain = ?, crm_type = ? WHERE state = ?",
			encClientID, encClientSecret, encRedirectURL, encSubdomain, r.crmType, r.state)
		if err != nil {
			return fmt.Errorf("EncryptCRMOAuthStatesWSS: update: %w", err)
		}
		encrypted++
	}

	progressCallback(fmt.Sprintf("ENCRYPTED_OAUTH_STATES_COUNT:%d", encrypted))
	return nil
}

func (d *DB) EncryptUserStorageConfigWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT access_key_ciphertext, secret_key_ciphertext FROM user_storage_config WHERE user_id = ?", userId)
	if err != nil {
		return fmt.Errorf("EncryptUserStorageConfigWSS: query: %w", err)
	}
	defer rows.Close()

	type record struct {
		accessKeyCiphertext sql.NullString
		secretKeyCiphertext sql.NullString
	}

	var toEncrypt []record
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.accessKeyCiphertext, &r.secretKeyCiphertext); err != nil {
			return fmt.Errorf("EncryptUserStorageConfigWSS: scan: %w", err)
		}

		// Пропускаем уже зашифрованные
		if (!r.accessKeyCiphertext.Valid || r.accessKeyCiphertext.String == "" || crypto.IsEncryptedWithMasterKey(r.accessKeyCiphertext.String)) &&
			(!r.secretKeyCiphertext.Valid || r.secretKeyCiphertext.String == "" || crypto.IsEncryptedWithMasterKey(r.secretKeyCiphertext.String)) {
			continue
		}

		toEncrypt = append(toEncrypt, r)
	}

	if len(toEncrypt) == 0 {
		progressCallback("NO_USER_STORAGE_CONFIGS_TO_ENCRYPT")
		return nil
	}

	progressCallback(fmt.Sprintf("ENCRYPTING_USER_STORAGE_CONFIGS:%d", len(toEncrypt)))

	encrypted := 0
	for _, r := range toEncrypt {
		var encAccessKeyCiphertext, encSecretKeyCiphertext interface{}
		if r.accessKeyCiphertext.Valid && r.accessKeyCiphertext.String != "" {
			encAccessKeyCiphertext, _ = crypto.EncryptFieldWithMasterKey(masterKey, r.accessKeyCiphertext.String)
		}
		if r.secretKeyCiphertext.Valid && r.secretKeyCiphertext.String != "" {
			encSecretKeyCiphertext, _ = crypto.EncryptFieldWithMasterKey(masterKey, r.secretKeyCiphertext.String)
		}

		_, err = d.Conn().ExecContext(ctx,
			"UPDATE user_storage_config SET access_key_ciphertext = ?, secret_key_ciphertext = ? WHERE user_id = ?",
			encAccessKeyCiphertext, encSecretKeyCiphertext, userId)
		if err != nil {
			return fmt.Errorf("EncryptUserStorageConfigWSS: update: %w", err)
		}
		encrypted++
	}

	progressCallback(fmt.Sprintf("ENCRYPTED_USER_STORAGE_CONFIGS_COUNT:%d", encrypted))
	return nil
}

func (d *DB) EncryptUserStorageServicesBotDataWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error {
	ctx, cancel := context.WithTimeout(d.Context(), 120*time.Second)
	defer cancel()

	// Универсальная функция для одной таблицы
	encryptTable := func(table string) error {
		rows, err := d.Conn().QueryContext(ctx,
			fmt.Sprintf("SELECT Id, AuthData FROM %s WHERE UserId = ?", table), userId)
		if err != nil {
			return fmt.Errorf("EncryptUserStorageServicesBotDataWSS: query %s: %w", table, err)
		}
		defer rows.Close()

		type record struct {
			id       int
			authData sql.NullString
		}

		var toEncrypt []record
		for rows.Next() {
			var r record
			if err = rows.Scan(&r.id, &r.authData); err != nil {
				return fmt.Errorf("EncryptUserStorageServicesBotDataWSS: scan %s: %w", table, err)
			}

			// Пропускаем пустые или уже зашифрованные
			if !r.authData.Valid || r.authData.String == "" || crypto.IsEncryptedWithMasterKey(r.authData.String) {
				continue
			}
			toEncrypt = append(toEncrypt, r)
		}

		if len(toEncrypt) == 0 {
			// NO_SERVICE_TGBOTS_TO_ENCRYPT или NO_SERVICE_WABOTS_TO_ENCRYPT
			progressCallback(fmt.Sprintf("NO_%s_TO_ENCRYPT", strings.ToUpper(table)))
			return nil
		}

		// ENCRYPTING_SERVICE_TGBOTS: ИЛИ ENCRYPTING_SERVICE_WABOTS:
		progressCallback(fmt.Sprintf("ENCRYPTING_%s:%d", strings.ToUpper(table), len(toEncrypt)))

		encrypted := 0
		for _, r := range toEncrypt {
			encAuthData, _ := crypto.EncryptFieldWithMasterKey(masterKey, r.authData.String)

			_, err = d.Conn().ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET AuthData = ? WHERE Id = ?", table),
				encAuthData, r.id)
			if err != nil {
				return fmt.Errorf("EncryptUserStorageServicesBotDataWSS: update %s: %w", table, err)
			}
			encrypted++
		}

		// ENCRYPTED_SERVICE_TGBOTS_COUNT: ИЛИ ENCRYPTED_SERVICE_WABOTS_COUNT:
		progressCallback(fmt.Sprintf("ENCRYPTED_%s_COUNT:%d", strings.ToUpper(table), encrypted))
		return nil
	}

	// Шифруем обе таблицы
	if err := encryptTable("service_tgbots"); err != nil {
		return err
	}
	if err := encryptTable("service_wabots"); err != nil {
		return err
	}

	return nil
}
