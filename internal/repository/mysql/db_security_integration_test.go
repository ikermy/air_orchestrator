package db

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

	"github.com/ikermy/air_common/pkg/crypto"
	"github.com/ikermy/air_common/pkg/mode"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

const testEncryptChannelsUserID uint32 = 23

func initDBAndExamForSecurityIntegrationTest(t *testing.T) (*DB, *exam.Exam, context.CancelFunc) {
	t.Helper()

	logger.StdOut().WithLogLevel(logger.DEBUG).Apply()

	// ── Redis ───────────────────────────────────────────────────────────────────
	state.RedisAddr = os.Getenv("REDIS_ADDR")
	state.RedisPassword = os.Getenv("REDIS_PASSWORD")
	dbStr := os.Getenv("REDIS_DB")
	if dbStr != "" {
		if n, err := strconv.Atoi(dbStr); err == nil {
			state.RedisDB = n
		}
	}
	if state.RedisAddr != "" {
		logger.Info("Redis: адрес=%s, db=%d", state.RedisAddr, state.RedisDB)
	} else {
		logger.Info("Redis: не настроен (REDIS_ADDR пуст)")
	}

	if err := loadAppMasterKeyForSecurityIntegrationTest(); err != nil {
		t.Fatalf("ошибка загрузки APP_MASTER_KEY для теста: %v", err)
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

	if state.RedisAddr == "" {
		_ = d.Close()
		cancel()
		t.Fatalf("redis не настроен: state.RedisAddr пустой")
	}

	redisCli, err := rediscache.New(ctx, state.RedisAddr, state.RedisPassword, state.RedisDB)
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
		t.Fatalf("master key не найден в cache/redis для userID=%d", testEncryptChannelsUserID)
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

func readChannelColumns(t *testing.T, d *DB, userID uint32) (channelSnapshot, error) {
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
