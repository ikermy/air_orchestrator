package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ikermy/air-common/v2/pkg/mode"
)

// AirDevice — зарегистрированное устройство air_phantom.
type AirDevice struct {
	DeviceID        string
	UserID          uint64
	Name            string
	OS              string
	Arch            string
	OSVersion       string
	HardwareHash    string
	TokenHash       string
	TokenPrefix     string
	Status          string
	OutputMode      string
	AgentVersion    string
	ProtocolVersion int
	Capabilities    string
	ConfigVersion   int64
	LastSeenAt      *time.Time
	LastError       string
	CreatedAt       time.Time
	RevokedAt       *time.Time
}

// AirSession — сессия web-viewer (телефон/планшет).
type AirSession struct {
	SessionID   string
	UserID      uint64
	DeviceID    string
	PairingHash string
	Status      string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	ClaimedAt   *time.Time
}

// AirEnrollInput — данные, которые присылает агент при enrollment.
type AirEnrollInput struct {
	Name            string
	OS              string
	Arch            string
	OSVersion       string
	HardwareHash    string
	AgentVersion    string
	ProtocolVersion int
	Capabilities    string
	OutputMode      string
}

// AirHeartbeatInput — heartbeat агента.
type AirHeartbeatInput struct {
	AgentVersion    string
	ProtocolVersion int
	Capabilities    string
	OutputMode      string
	LastError       string
}

func airCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, mode.GetSQLTimeToCancel())
}

func scanAirDevice(sc interface {
	Scan(dest ...any) error
}) (*AirDevice, error) {
	var (
		d        AirDevice
		lastSeen sql.NullTime
		revoked  sql.NullTime
		caps     sql.NullString
	)
	err := sc.Scan(
		&d.DeviceID, &d.UserID, &d.Name, &d.OS, &d.Arch, &d.OSVersion,
		&d.HardwareHash, &d.TokenHash, &d.TokenPrefix, &d.Status, &d.OutputMode,
		&d.AgentVersion, &d.ProtocolVersion, &caps, &d.ConfigVersion,
		&lastSeen, &d.LastError, &d.CreatedAt, &revoked,
	)
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		d.LastSeenAt = &t
	}
	if revoked.Valid {
		t := revoked.Time
		d.RevokedAt = &t
	}
	if caps.Valid {
		d.Capabilities = caps.String
	}
	return &d, nil
}

const airDeviceColumns = `DeviceId, UserId, Name, Os, Arch, OsVersion, HardwareHash,
	TokenHash, TokenPrefix, Status, OutputMode, AgentVersion, ProtocolVersion,
	Capabilities, ConfigVersion, LastSeenAt, LastError, CreatedAt, RevokedAt`

// AirCreateEnrollment создаёт одноразовый код привязки (хранится как hash).
func (i *Implementation) AirCreateEnrollment(ctx context.Context, userID uint64, codeHash string, expiresAt time.Time) error {
	if userID == 0 || codeHash == "" {
		return fmt.Errorf("air enrollment: некорректные параметры")
	}
	ctx, cancel := airCtx(ctx)
	defer cancel()

	_, err := i.Conn().ExecContext(ctx,
		`INSERT INTO air_enrollment_code (CodeHash, UserId, ExpiresAt) VALUES (?, ?, ?)`,
		codeHash, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("air enrollment insert: %w", err)
	}
	return nil
}

// AirEnrollDevice обменивает код на устройство и device token.
func (i *Implementation) AirEnrollDevice(ctx context.Context, codeHash string, in AirEnrollInput, tokenHash, tokenPrefix string) (*AirDevice, error) {
	if codeHash == "" || tokenHash == "" {
		return nil, fmt.Errorf("air enroll: некорректные параметры")
	}
	ctx, cancel := airCtx(ctx)
	defer cancel()

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("air enroll begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		userID    uint64
		expiresAt time.Time
		usedAt    sql.NullTime
		deviceID  sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT UserId, ExpiresAt, UsedAt, DeviceId
		   FROM air_enrollment_code WHERE CodeHash = ? FOR UPDATE`, codeHash).
		Scan(&userID, &expiresAt, &usedAt, &deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("air enroll: код не найден")
		}
		return nil, fmt.Errorf("air enroll select: %w", err)
	}
	if usedAt.Valid {
		return nil, fmt.Errorf("air enroll: код уже использован")
	}
	if time.Now().After(expiresAt) {
		return nil, fmt.Errorf("air enroll: код истёк")
	}

	id := uuid.NewString()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO air_device
		 (DeviceId, UserId, Name, Os, Arch, OsVersion, HardwareHash, TokenHash, TokenPrefix,
		  Status, OutputMode, AgentVersion, ProtocolVersion, Capabilities)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
		id, userID, in.Name, in.OS, in.Arch, in.OSVersion, in.HardwareHash, tokenHash, tokenPrefix,
		airDefault(in.OutputMode, "both"), in.AgentVersion, airDefaultInt(in.ProtocolVersion, 1), airNullJSON(in.Capabilities))
	if err != nil {
		return nil, fmt.Errorf("air enroll insert device: %w", err)
	}

	if _, err = tx.ExecContext(ctx,
		`UPDATE air_enrollment_code SET UsedAt = NOW(), DeviceId = ? WHERE CodeHash = ?`,
		id, codeHash); err != nil {
		return nil, fmt.Errorf("air enroll mark code: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("air enroll commit: %w", err)
	}

	return i.AirDeviceByID(ctx, id)
}

// AirDeviceByTokenHash ищет устройство по хэшу device token.
func (i *Implementation) AirDeviceByTokenHash(ctx context.Context, tokenHash string) (*AirDevice, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	row := i.Conn().QueryRowContext(ctx,
		`SELECT `+airDeviceColumns+` FROM air_device WHERE TokenHash = ?`, tokenHash)
	d, err := scanAirDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("air device by token: %w", err)
	}
	return d, nil
}

// AirDeviceByID возвращает устройство по id.
func (i *Implementation) AirDeviceByID(ctx context.Context, deviceID string) (*AirDevice, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	row := i.Conn().QueryRowContext(ctx,
		`SELECT `+airDeviceColumns+` FROM air_device WHERE DeviceId = ?`, deviceID)
	d, err := scanAirDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("air device by id: %w", err)
	}
	return d, nil
}

// AirHeartbeat обновляет last_seen и вытесняет другие онлайн-устройства пользователя.
func (i *Implementation) AirHeartbeat(ctx context.Context, deviceID string, in AirHeartbeatInput) error {
	ctx, cancel := airCtx(ctx)
	defer cancel()

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("air heartbeat begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID uint64
	if err := tx.QueryRowContext(ctx,
		`SELECT UserId FROM air_device WHERE DeviceId = ? AND Status = 'active' FOR UPDATE`,
		deviceID).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("air heartbeat: устройство не найдено")
		}
		return fmt.Errorf("air heartbeat select: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE air_device SET
		   AgentVersion = ?, ProtocolVersion = ?, Capabilities = ?, OutputMode = ?,
		   LastError = ?, LastSeenAt = NOW()
		 WHERE DeviceId = ?`,
		in.AgentVersion, airDefaultInt(in.ProtocolVersion, 1), airNullJSON(in.Capabilities),
		airDefault(in.OutputMode, "both"), in.LastError, deviceID); err != nil {
		return fmt.Errorf("air heartbeat update: %w", err)
	}

	// Один онлайн-пользователь: остальные устройства считаем offline и закрываем их сессии.
	if _, err := tx.ExecContext(ctx,
		`UPDATE air_device SET LastSeenAt = NULL
		 WHERE UserId = ? AND DeviceId <> ? AND LastSeenAt IS NOT NULL`, userID, deviceID); err != nil {
		return fmt.Errorf("air heartbeat kick devices: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE air_session SET Status = 'closed'
		 WHERE UserId = ? AND Status = 'active' AND DeviceId <> ?`, userID, deviceID); err != nil {
		return fmt.Errorf("air heartbeat kick sessions: %w", err)
	}

	return tx.Commit()
}

// AirGetDeviceConfig возвращает серверную часть конфигурации и её версию.
func (i *Implementation) AirGetDeviceConfig(ctx context.Context, deviceID string) (string, int64, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	var (
		raw     string
		version int64
	)
	err := i.Conn().QueryRowContext(ctx,
		`SELECT ConfigJSON, Version FROM air_device_config WHERE DeviceId = ?`, deviceID).
		Scan(&raw, &version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "{}", 0, nil
		}
		return "", 0, fmt.Errorf("air device config: %w", err)
	}
	return raw, version, nil
}

// AirUpsertDeviceConfig сохраняет серверную конфигурацию устройства.
func (i *Implementation) AirUpsertDeviceConfig(ctx context.Context, deviceID, configJSON string) error {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	_, err := i.Conn().ExecContext(ctx,
		`INSERT INTO air_device_config (DeviceId, ConfigJSON, Version)
		 VALUES (?, ?, 1)
		 ON DUPLICATE KEY UPDATE ConfigJSON = VALUES(ConfigJSON), Version = Version + 1`,
		deviceID, configJSON)
	if err != nil {
		return fmt.Errorf("air device config upsert: %w", err)
	}
	return nil
}

// AirListDevices возвращает все устройства пользователя.
func (i *Implementation) AirListDevices(ctx context.Context, userID uint64) ([]AirDevice, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	rows, err := i.Conn().QueryContext(ctx,
		`SELECT `+airDeviceColumns+` FROM air_device WHERE UserId = ? ORDER BY CreatedAt DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("air list devices: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AirDevice
	for rows.Next() {
		d, err := scanAirDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("air list devices scan: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// AirRevokeDevice отзывает устройство пользователя.
func (i *Implementation) AirRevokeDevice(ctx context.Context, userID uint64, deviceID string) error {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	res, err := i.Conn().ExecContext(ctx,
		`UPDATE air_device SET Status = 'revoked', RevokedAt = NOW(), LastSeenAt = NULL
		 WHERE DeviceId = ? AND UserId = ? AND Status <> 'revoked'`, deviceID, userID)
	if err != nil {
		return fmt.Errorf("air revoke device: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("air revoke device: не найдено")
	}
	_, _ = i.Conn().ExecContext(ctx,
		`UPDATE air_session SET Status = 'closed' WHERE DeviceId = ? AND Status IN ('pending','active')`,
		deviceID)
	return nil
}

// AirForgetDevice отзывает устройство по device token (команда "удалить данные").
func (i *Implementation) AirForgetDevice(ctx context.Context, deviceID string) error {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	if _, err := i.Conn().ExecContext(ctx,
		`UPDATE air_device SET Status = 'revoked', RevokedAt = NOW(), LastSeenAt = NULL
		 WHERE DeviceId = ? AND Status <> 'revoked'`, deviceID); err != nil {
		return fmt.Errorf("air forget device: %w", err)
	}
	_, _ = i.Conn().ExecContext(ctx,
		`UPDATE air_session SET Status = 'closed' WHERE DeviceId = ? AND Status IN ('pending','active')`,
		deviceID)
	return nil
}

// AirCreateSession создаёт сессию web-viewer.
func (i *Implementation) AirCreateSession(ctx context.Context, userID uint64, deviceID, pairingHash string, expiresAt time.Time) (string, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()

	var ownerID uint64
	if err := i.Conn().QueryRowContext(ctx,
		`SELECT UserId FROM air_device WHERE DeviceId = ? AND Status = 'active'`, deviceID).
		Scan(&ownerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("air session: устройство не найдено")
		}
		return "", fmt.Errorf("air session select device: %w", err)
	}
	if ownerID != userID {
		return "", fmt.Errorf("air session: чужое устройство")
	}

	// Одна активная viewer-сессия на устройство: закрываем предыдущие.
	if _, err := i.Conn().ExecContext(ctx,
		`UPDATE air_session SET Status = 'closed'
		 WHERE DeviceId = ? AND Status IN ('pending','active')`, deviceID); err != nil {
		return "", fmt.Errorf("air session close previous: %w", err)
	}

	id := uuid.NewString()
	_, err := i.Conn().ExecContext(ctx,
		`INSERT INTO air_session (SessionId, UserId, DeviceId, PairingHash, Status, ExpiresAt)
		 VALUES (?, ?, ?, ?, 'pending', ?)`,
		id, userID, deviceID, pairingHash, expiresAt)
	if err != nil {
		return "", fmt.Errorf("air session insert: %w", err)
	}
	return id, nil
}

// AirClaimSession активирует сессию после ввода pairing code.
func (i *Implementation) AirClaimSession(ctx context.Context, userID uint64, sessionID, pairingHash string) (*AirSession, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("air claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		s         AirSession
		deviceID  sql.NullString
		claimedAt sql.NullTime
	)
	err = tx.QueryRowContext(ctx,
		`SELECT SessionId, UserId, DeviceId, PairingHash, Status, ExpiresAt, CreatedAt, ClaimedAt
		   FROM air_session WHERE SessionId = ? FOR UPDATE`, sessionID).
		Scan(&s.SessionID, &s.UserID, &deviceID, &s.PairingHash, &s.Status, &s.ExpiresAt, &s.CreatedAt, &claimedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("air claim: сессия не найдена")
		}
		return nil, fmt.Errorf("air claim select: %w", err)
	}
	if deviceID.Valid {
		s.DeviceID = deviceID.String
	}
	if s.UserID != userID {
		return nil, fmt.Errorf("air claim: чужая сессия")
	}
	if time.Now().After(s.ExpiresAt) {
		return nil, fmt.Errorf("air claim: сессия истекла")
	}
	if s.PairingHash != pairingHash {
		return nil, fmt.Errorf("air claim: неверный код")
	}
	// Идемпотентность для reconnect/refresh: уже активная сессия с тем же кодом.
	if s.Status == "active" {
		_ = tx.Rollback()
		return &s, nil
	}
	if s.Status != "pending" {
		return nil, fmt.Errorf("air claim: сессия уже использована")
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE air_session SET Status = 'active', ClaimedAt = NOW() WHERE SessionId = ?`, sessionID); err != nil {
		return nil, fmt.Errorf("air claim update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("air claim commit: %w", err)
	}
	s.Status = "active"
	now := time.Now()
	s.ClaimedAt = &now
	return &s, nil
}

// AirCloseSession закрывает сессию.
func (i *Implementation) AirCloseSession(ctx context.Context, userID uint64, sessionID string) error {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	_, err := i.Conn().ExecContext(ctx,
		`UPDATE air_session SET Status = 'closed' WHERE SessionId = ? AND UserId = ?`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("air close session: %w", err)
	}
	return nil
}

// AirListSessions возвращает сессии пользователя.
func (i *Implementation) AirListSessions(ctx context.Context, userID uint64, limit int) ([]AirSession, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	ctx, cancel := airCtx(ctx)
	defer cancel()
	rows, err := i.Conn().QueryContext(ctx,
		`SELECT SessionId, UserId, DeviceId, PairingHash, Status, ExpiresAt, CreatedAt, ClaimedAt
		   FROM air_session WHERE UserId = ? ORDER BY CreatedAt DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("air list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AirSession
	for rows.Next() {
		var (
			s         AirSession
			deviceID  sql.NullString
			claimedAt sql.NullTime
		)
		if err := rows.Scan(&s.SessionID, &s.UserID, &deviceID, &s.PairingHash, &s.Status,
			&s.ExpiresAt, &s.CreatedAt, &claimedAt); err != nil {
			return nil, fmt.Errorf("air list sessions scan: %w", err)
		}
		if deviceID.Valid {
			s.DeviceID = deviceID.String
		}
		if claimedAt.Valid {
			t := claimedAt.Time
			s.ClaimedAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AirSessionByID возвращает сессию без проверки владельца (для gRPC relay).
func (i *Implementation) AirSessionByID(ctx context.Context, sessionID string) (*AirSession, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	var (
		s         AirSession
		deviceID  sql.NullString
		claimedAt sql.NullTime
	)
	err := i.Conn().QueryRowContext(ctx,
		`SELECT SessionId, UserId, DeviceId, PairingHash, Status, ExpiresAt, CreatedAt, ClaimedAt
		   FROM air_session WHERE SessionId = ?`, sessionID).
		Scan(&s.SessionID, &s.UserID, &deviceID, &s.PairingHash, &s.Status, &s.ExpiresAt, &s.CreatedAt, &claimedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("air session by id: %w", err)
	}
	if deviceID.Valid {
		s.DeviceID = deviceID.String
	}
	if claimedAt.Valid {
		t := claimedAt.Time
		s.ClaimedAt = &t
	}
	return &s, nil
}

// AirActiveSessionByDevice возвращает активную сессию устройства.
func (i *Implementation) AirActiveSessionByDevice(ctx context.Context, deviceID string) (*AirSession, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	var (
		s         AirSession
		claimedAt sql.NullTime
	)
	err := i.Conn().QueryRowContext(ctx,
		`SELECT SessionId, UserId, DeviceId, PairingHash, Status, ExpiresAt, CreatedAt, ClaimedAt
		   FROM air_session WHERE DeviceId = ? AND Status = 'active' ORDER BY CreatedAt DESC LIMIT 1`, deviceID).
		Scan(&s.SessionID, &s.UserID, &s.DeviceID, &s.PairingHash, &s.Status, &s.ExpiresAt, &s.CreatedAt, &claimedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("air active session: %w", err)
	}
	if claimedAt.Valid {
		t := claimedAt.Time
		s.ClaimedAt = &t
	}
	return &s, nil
}

// AirSession возвращает сессию пользователя.
func (i *Implementation) AirSession(ctx context.Context, userID uint64, sessionID string) (*AirSession, error) {
	ctx, cancel := airCtx(ctx)
	defer cancel()
	var (
		s         AirSession
		deviceID  sql.NullString
		claimedAt sql.NullTime
	)
	err := i.Conn().QueryRowContext(ctx,
		`SELECT SessionId, UserId, DeviceId, PairingHash, Status, ExpiresAt, CreatedAt, ClaimedAt
		   FROM air_session WHERE SessionId = ? AND UserId = ?`, sessionID, userID).
		Scan(&s.SessionID, &s.UserID, &deviceID, &s.PairingHash, &s.Status, &s.ExpiresAt, &s.CreatedAt, &claimedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("air session: %w", err)
	}
	if deviceID.Valid {
		s.DeviceID = deviceID.String
	}
	if claimedAt.Valid {
		t := claimedAt.Time
		s.ClaimedAt = &t
	}
	return &s, nil
}

func airDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func airDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func airNullJSON(v string) any {
	if v == "" {
		return nil
	}
	return v
}
