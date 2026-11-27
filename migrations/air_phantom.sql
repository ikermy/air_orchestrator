-- air_phantom service schema.
-- Apply after the base air schema. No FK constraints: user/device tables may be
-- sharded or migrated independently; integrity is enforced in the usecase.

CREATE TABLE IF NOT EXISTS air_device (
  DeviceId        CHAR(36)        NOT NULL PRIMARY KEY,
  UserId          BIGINT UNSIGNED NOT NULL,
  Name            VARCHAR(120)    NOT NULL DEFAULT '',
  Os              ENUM('windows','linux','macos') NOT NULL DEFAULT 'windows',
  Arch            ENUM('amd64','arm64')           NOT NULL DEFAULT 'amd64',
  OsVersion       VARCHAR(64)     NOT NULL DEFAULT '',
  HardwareHash    CHAR(64)        NOT NULL DEFAULT '',
  TokenHash       CHAR(64)        NOT NULL,
  TokenPrefix     VARCHAR(12)     NOT NULL DEFAULT '',
  Status          ENUM('pending','active','revoked') NOT NULL DEFAULT 'pending',
  OutputMode      ENUM('overlay','web','both')    NOT NULL DEFAULT 'both',
  AgentVersion    VARCHAR(32)     NOT NULL DEFAULT '',
  ProtocolVersion INT UNSIGNED    NOT NULL DEFAULT 1,
  Capabilities    JSON            NULL,
  ConfigVersion   BIGINT UNSIGNED NOT NULL DEFAULT 1,
  BlockedBy       CHAR(36)        NOT NULL DEFAULT '',
  LastSeenAt      DATETIME        NULL,
  LastError       VARCHAR(512)    NOT NULL DEFAULT '',
  CreatedAt       DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  RevokedAt       DATETIME        NULL,
  UNIQUE KEY uk_air_device_token (TokenHash),
  KEY idx_air_device_user (UserId, Status),
  KEY idx_air_device_os (Os, Arch)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS air_enrollment_code (
  CodeHash   CHAR(64)        NOT NULL PRIMARY KEY,
  UserId     BIGINT UNSIGNED NOT NULL,
  ExpiresAt  DATETIME        NOT NULL,
  UsedAt     DATETIME        NULL,
  DeviceId   CHAR(36)        NULL,
  CreatedAt  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_air_enroll_user (UserId, ExpiresAt)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS air_session (
  SessionId   CHAR(36)        NOT NULL PRIMARY KEY,
  UserId      BIGINT UNSIGNED NOT NULL,
  DeviceId    CHAR(36)        NULL,
  PairingHash CHAR(64)        NOT NULL,
  Status      ENUM('pending','active','closed','expired') NOT NULL DEFAULT 'pending',
  ExpiresAt   DATETIME        NOT NULL,
  CreatedAt   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ClaimedAt   DATETIME        NULL,
  KEY idx_air_session_user (UserId, Status),
  KEY idx_air_session_device (DeviceId, Status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS air_device_config (
  DeviceId    CHAR(36)        NOT NULL PRIMARY KEY,
  ConfigJSON  JSON            NOT NULL,
  Version     BIGINT UNSIGNED NOT NULL DEFAULT 1,
  UpdatedAt   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- air_phantom is an add-on service in the dashboard service list.
-- Keep existing enum values; re-running this ALTER is idempotent.
ALTER TABLE `service`
  MODIFY COLUMN `ServiceType` ENUM('lead-haunter','voice-call','phantom') NOT NULL;
