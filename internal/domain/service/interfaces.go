// Package service содержит интерфейсы прикладных сервисов — абстракции
// инфраструктурного слоя (безопасность, почта и т.д.).
// Реализации находятся в internal/infrastructure/.
package service

import (
	"context"
	"crypto/ed25519"
	"time"
)

// SecurityService — интерфейс сервиса безопасности (JWT, шифрование email,
// хэширование паролей, TOTP, MasterKey), реализуется *exam.Exam.
type SecurityService interface {
	AddRegUser(respId uint64) (string, error)
	DecryptPassword(respId uint64, key string) (string, error)
	HashPassword(plain string) (string, error)
	VerifyPassword(storedHash, plain string) bool
	EncryptEmail(email string) (string, error)
	DecryptEmailIfNeeded(raw string) (string, error)
	EmailHMAC(email string) string
	DecryptEmailInJSON(raw []byte, path string) ([]byte, error)
	GenerateAccessToken(userId uint32, respId uint64) (sta, lta string, err error)
	GetMailConfirmationToken(userId uint32, email string) (string, error)
	ParseMailConfirmationToken(tokenString string) (uint32, string, error)
	ParseAuthToken(tokenString *string) (*uint32, *uint64, error)
	VerifyAccessToken(tokenString string) (uint32, uint64, error)
	BlacklistToken(ctx context.Context, token string, expiration time.Duration) error
	IsBlacklisted(ctx context.Context, token string) (bool, error)
	GetTimeCreatedSessionKey() string
	SetNewSessionKey() error
	// TOTP
	GenerateTOTPSecret(accountName string) (secret, uri string, err error)
	EncryptTOTPSecret(secret string) (string, error)
	ValidateTOTPCode(encryptedSecret, code string) bool
	// MasterKey
	GenerateAndWrapMasterKey(userId uint32, password string) (rawB64, encMK, wrapSalt string, err error)
	WrapMasterKey(rawB64, password string) (encMK, wrapSalt string, err error)
	LoadMasterKey(userId uint32, password, encMK, wrapSalt string) error
	GetMasterKey(userId uint32) ([32]byte, bool)
	RefreshMasterKeyTTL(userId uint32) error
	// Widget methods
	WidgetNewToken(userID uint32, respID uint64, origin string, expired time.Duration) (string, error)
	GenerateEd25519KeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error)
}

// MailerService — интерфейс почтового сервиса, реализуется *smtp.SMTP.
type MailerService interface {
	SendConfirmMail(lang, recMail, link string) error
	SendResetPasswordMail(lang, recipient, resetToken string) error
	SendCarpinteroVerification(userId uint64, message string) error
	SendNotificationMail(userId uint32, recipient, msg string) error
}
