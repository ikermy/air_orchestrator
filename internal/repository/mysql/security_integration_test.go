//go:build integration

package mysql

import (
	"air_orchestrator/internal/domain/state"
	rediscache "air_orchestrator/internal/infrastructure/redis"
	exam "air_orchestrator/internal/infrastructure/security"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/crypto"
	"github.com/ikermy/air-common/v2/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

const testEncryptChannelsUserID uint64 = 23

func initDBAndExamForSecurityIntegrationTest(t *testing.T) (*Implementation, *exam.Exam, context.CancelFunc) {
	t.Helper()

	logger.StdOut().WithLogLevel(logger.DEBUG).Apply()

	// ── Redis ───────────────────────────────────────────────────────────────────
	redisCfg := state.Redis{
		RedisAddr:     os.Getenv("REDIS_ADDR"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisDB:       0,
	}
	dbStr := os.Getenv("REDIS_DB")
	if dbStr != "" {
		if n, err := strconv.Atoi(dbStr); err == nil {
			redisCfg.RedisDB = n
		}
	}
	if redisCfg.RedisAddr != "" {
		logger.Info("Redis: адрес=%s, db=%d", redisCfg.RedisAddr, redisCfg.RedisDB)
	} else {
		logger.Info("Redis: не настроен (REDIS_ADDR пуст)")
	}

	if err := loadAppMasterKeyForSecurityIntegrationTest(); err != nil {
		t.Skipf("security integration test requires APP_MASTER_KEY_FILE: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	d, err := New(ctx)
	if err != nil {
		cancel()
		t.Fatalf("ошибка подключения к БД: %v", err)
	}

	x := exam.New()
	if err := x.LoadOrInitKey(ctx, d); err != nil {
		_ = d.Close()
		cancel()
		t.Fatalf("ошибка инициализации session key: %v", err)
	}

	if redisCfg.RedisAddr == "" {
		_ = d.Close()
		cancel()
		t.Skipf("security integration test requires REDIS_ADDR")
	}

	redisCli, err := rediscache.New(ctx, redisCfg.RedisAddr, redisCfg.RedisPassword, redisCfg.RedisDB)
	if err != nil {
		_ = d.Close()
		cancel()
		t.Fatalf("ошибка подключения к Redis: %v", err)
	}

	x.SetRedisClient(redisCli)
	if err := x.LoadAllMasterKeysFromRedis(ctx); err != nil {
		_ = redisCli.Close()
		_ = d.Close()
		cancel()
		t.Fatalf("ошибка загрузки master keys из Redis: %v", err)
	}

	cleanup := func() {
		if err := redisCli.Close(); err != nil {
			t.Logf("redis close warning: %v", err)
		}
		if err := d.Close(); err != nil {
			t.Logf("db close warning: %v", err)
		}
		cancel()
	}

	return d, x, cleanup
}

func loadAppMasterKeyForSecurityIntegrationTest() error {
	if len(state.MasterKey) > 0 {
		return nil
	}

	masterKeyPath := strings.TrimSpace(os.Getenv("APP_MASTER_KEY_FILE"))
	if masterKeyPath == "" {
		return fmt.Errorf("APP_MASTER_KEY_FILE не установлен")
	}

	data, err := os.ReadFile(masterKeyPath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать APP_MASTER_KEY_FILE: %w", err)
	}

	state.MasterKey = []byte(strings.TrimSpace(string(data)))
	if len(state.MasterKey) == 0 {
		return fmt.Errorf("APP_MASTER_KEY_FILE пуст")
	}

	return nil
}

func TestEncryptChannelsWSSManual(t *testing.T) {
	d, x, cleanup := initDBAndExamForSecurityIntegrationTest(t)
	defer cleanup()

	mk, ok := x.GetMasterKey(testEncryptChannelsUserID)
	if !ok {
		t.Skipf("master key не найден в cache/redis для userID=%d", testEncryptChannelsUserID)
	}

	t.Logf("master key найден для userID=%d", testEncryptChannelsUserID)

	before, err := readChannelColumns(t, d, testEncryptChannelsUserID)
	if err != nil {
		t.Fatalf("ошибка чтения каналов до шифрования: %v", err)
	}
	t.Logf("каналы до шифрования: %s", formatChannelSnapshot(before))

	var progress []string
	err = d.EncryptChannelsWSS(testEncryptChannelsUserID, mk, func(msg string) {
		progress = append(progress, msg)
		t.Logf("EncryptChannelsWSS progress: %s", msg)
	})
	if err != nil {
		t.Fatalf("EncryptChannelsWSS failed: %v", err)
	}

	after, err := readChannelColumns(t, d, testEncryptChannelsUserID)
	if err != nil {
		t.Fatalf("ошибка чтения каналов после шифрования: %v", err)
	}
	t.Logf("каналы после шифрования: %s", formatChannelSnapshot(after))

	assertChannelsEncryptedState(t, before, after, progress)
}

type channelSnapshot struct {
	TgBot     sql.NullString
	Widget    sql.NullString
	TgUserBot sql.NullString
	Whats     sql.NullString
	Insta     sql.NullString
	Avito     sql.NullString
}

func readChannelColumns(t *testing.T, d *Implementation, userID uint64) (channelSnapshot, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var snap channelSnapshot
	err := d.Conn().QueryRowContext(ctx,
		`SELECT TgBot, Widget, TgUserBot, Whats, Insta, Avito FROM channels WHERE UserId = ?`, userID,
	).Scan(&snap.TgBot, &snap.Widget, &snap.TgUserBot, &snap.Whats, &snap.Insta, &snap.Avito)
	if err != nil {
		return channelSnapshot{}, err
	}

	return snap, nil
}

func TestEncryptChannelsUserConfigsWSSLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		testUserID      uint64 = 999999996
		testChannelID   uint64 = 999999996000001
		plaintextConfig        = `{"token":"integration-test"}`
	)

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_channel_configs WHERE user_id=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_channels WHERE user_id=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM channels WHERE UserId=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-security-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_channels (id, user_id, channel_type, enabled) VALUES (?, ?, 'widget', 1)`,
		testChannelID, testUserID); err != nil {
		t.Fatalf("create v2 channel: %v", err)
	}
	// Намеренно не создаём legacy-строку в channels: шифрование v2-конфигов
	// не должно зависеть от её наличия.
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_channel_configs (channel_id, user_id, config) VALUES (?, ?, ?)`,
		testChannelID, testUserID, plaintextConfig); err != nil {
		t.Fatalf("create v2 config: %v", err)
	}

	var masterKey [32]byte
	for i := range masterKey {
		masterKey[i] = byte(i + 1)
	}

	var progress []string
	if err := repo.EncryptChannelsWSS(testUserID, masterKey, func(msg string) {
		progress = append(progress, msg)
	}); err != nil {
		t.Fatalf("EncryptChannelsWSS: %v", err)
	}

	var encryptedConfig string
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT config FROM user_channel_configs WHERE channel_id=?`, testChannelID).Scan(&encryptedConfig); err != nil {
		t.Fatalf("read encrypted config: %v", err)
	}
	if !crypto.IsEncryptedWithMasterKey(encryptedConfig) {
		t.Fatalf("v2 config not encrypted: %q (progress=%v)", encryptedConfig, progress)
	}
	decrypted, err := crypto.DecryptFieldWithMasterKey(masterKey, encryptedConfig)
	if err != nil {
		t.Fatalf("decrypt v2 config: %v", err)
	}
	if decrypted != plaintextConfig {
		t.Fatalf("decrypted v2 config = %q, want %q", decrypted, plaintextConfig)
	}
}

func assertChannelsEncryptedState(t *testing.T, before, after channelSnapshot, progress []string) {
	t.Helper()

	type channelField struct {
		name   string
		before sql.NullString
		after  sql.NullString
	}

	fields := []channelField{
		{name: "tgbot", before: before.TgBot, after: after.TgBot},
		{name: "widget", before: before.Widget, after: after.Widget},
		{name: "tgubot", before: before.TgUserBot, after: after.TgUserBot},
		{name: "whatsbot", before: before.Whats, after: after.Whats},
		{name: "insta", before: before.Insta, after: after.Insta},
		{name: "avito", before: before.Avito, after: after.Avito},
	}

	var candidates int
	for _, field := range fields {
		if !field.before.Valid || field.before.String == "" || crypto.IsEncryptedWithMasterKey(field.before.String) {
			continue
		}
		candidates++
		if !field.after.Valid || field.after.String == "" {
			t.Fatalf("поле %s стало пустым после шифрования", field.name)
		}
		if !crypto.IsEncryptedWithMasterKey(field.after.String) {
			t.Fatalf("поле %s не зашифровано master key after EncryptChannelsWSS", field.name)
		}
	}

	if candidates == 0 {
		t.Log("plaintext-каналов для шифрования не найдено; проверяю progress сигнал")
		for _, msg := range progress {
			if msg == "NO_CHANNELS_TO_ENCRYPT" {
				return
			}
		}
		t.Fatalf("ожидался progress-сигнал NO_CHANNELS_TO_ENCRYPT, но получено: %v", progress)
	}

	t.Logf("успешно проверено plaintext-полей для шифрования: %d", candidates)
}

func formatChannelSnapshot(s channelSnapshot) string {
	return fmt.Sprintf(
		"TgBot=%s, Widget=%s, TgUserBot=%s, Whats=%s, Insta=%s, Avito=%s",
		formatNullString(s.TgBot),
		formatNullString(s.Widget),
		formatNullString(s.TgUserBot),
		formatNullString(s.Whats),
		formatNullString(s.Insta),
		formatNullString(s.Avito),
	)
}

func formatNullString(v sql.NullString) string {
	if !v.Valid {
		return "<NULL>"
	}
	if v.String == "" {
		return "<EMPTY>"
	}
	if crypto.IsEncryptedWithMasterKey(v.String) {
		return "<ENCRYPTED>"
	}
	return "<PLAINTEXT>"
}
