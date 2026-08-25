package exam

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/ikermy/air-common/pkg/rpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Claims структура для хранения данных в токене jwt Widget
type Claims struct {
	UserId uint32 `json:"userId"`
	ReapId uint64 `json:"respId"`
	Origin string `json:"origin"`
	JTI    string `json:"jti"`
	jwt.RegisteredClaims
}

type WidgetCodeClaims struct {
	UserID       uint32   `json:"uid"`
	ExamKey      string   `json:"examKey"`
	AllowedUrls  []string `json:"allowedUrls"`
	NeverExpires bool     `json:"neverExpires"`
	JTI          string   `json:"jti"`
	jwt.RegisteredClaims
}

func (e *Exam) WidgetNewToken(
	userID uint32,
	respID uint64,
	origin string,
	expired time.Duration,
) (string, error) {
	now := time.Now()

	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("generate widget token jti: %w", err)
	}

	claims := Claims{
		UserId: userID,
		ReapId: respID,
		Origin: origin,
		JTI:    hex.EncodeToString(jti),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add((expired) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(e.sessionKey)
	if err != nil {
		return "", fmt.Errorf("sign widget token: %w", err)
	}

	return signedToken, nil
}

func (e *Exam) WidgetParseToken(tokenString string) (*proto.WidgetTokenData, error) {
	// Парсим токен с кастомными Claims
	parsed, err := jwt.ParseWithClaims(tokenString, &Claims{},
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("неподдерживаемый метод подписи: %v", token.Header["alg"])
			}
			return e.sessionKey, nil // здесь нужно передавать ключ, например из Exam
		})
	if err != nil {
		return nil, err
	}

	// Проверяем claims
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return &proto.WidgetTokenData{
		UserId:         claims.UserId,
		RespId:         claims.ReapId,
		ExpiredSeconds: claims.ExpiresAt.Unix(),
		Origin:         claims.Origin,
		Jti:            claims.JTI,
	}, nil
}

// GenerateEd25519KeyPair генерирует пару ключей Ed25519
func (e *Exam) GenerateEd25519KeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

func parseEd25519PrivateKey(value string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("private key is not PEM")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not Ed25519 private key")
	}

	return privateKey, nil
}

func parseEd25519PublicKey(value string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("public key is not PEM")
	}

	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	publicKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not Ed25519 public key")
	}

	return publicKey, nil
}

// WidgetNewCode generates a signed JWT for a widget, using the provided data and cryptographic keys.
// Returns a signed JWT string or an error if input validation fails or signing encounters an issue.
func (e *Exam) WidgetNewCode(data *proto.WidgetCodeData, publicKey, privateKey string) (string, error) {
	if data == nil {
		return "", fmt.Errorf("widget code data is required")
	}
	userId := data.UserId
	examKey := data.ExamKey
	jti := data.Jti
	expiresAt := data.ExpiresAt
	neverExpires := data.NeverExpires
	allowedUrls := data.AllowedUrls
	if userId == 0 {
		return "", fmt.Errorf("userId is required")
	}
	if strings.TrimSpace(examKey) == "" {
		return "", fmt.Errorf("examKey is required")
	}
	if len(allowedUrls) == 0 {
		return "", fmt.Errorf("allowedUrls is required")
	}
	if neverExpires && expiresAt != 0 {
		return "", fmt.Errorf("expiresAt and neverExpires are mutually exclusive")
	}
	if !neverExpires && expiresAt <= time.Now().Unix() {
		return "", fmt.Errorf("expiresAt must be in the future")
	}

	pub, err := parseEd25519PublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("parse public key: %w", err)
	}

	priv, err := parseEd25519PrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}

	derivedPublicKey := priv.Public().(ed25519.PublicKey)
	if !bytes.Equal(pub, derivedPublicKey) {
		return "", fmt.Errorf("public and private keys do not match")
	}

	if jti == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate jti: %w", err)
		}
		jti = hex.EncodeToString(buf)
	}

	now := time.Now()

	claims := WidgetCodeClaims{
		UserID:       userId,
		ExamKey:      examKey,
		AllowedUrls:  allowedUrls,
		NeverExpires: neverExpires,
		JTI:          jti,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "air-orc",
			Audience:  jwt.ClaimStrings{"marusya-widget"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	if !neverExpires {
		claims.ExpiresAt = jwt.NewNumericDate(
			time.Unix(expiresAt, 0),
		)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "widget-code-v1"

	signedToken, err := token.SignedString(priv)
	if err != nil {
		return "", fmt.Errorf("sign widget code: %w", err)
	}

	return signedToken, nil
}

// WidgetParseCode parses a widget code token string using a given public key and returns the decoded WidgetCodeData.
// It validates the token using EdDSA signing method and checks the claims for user ID, exam key, JTI, audience, and expiration.
func (e *Exam) WidgetParseCode(tokenString, publicKey string) (*proto.WidgetCodeData, error) {
	var claims WidgetCodeClaims

	token, err := jwt.ParseWithClaims(
		tokenString,
		&claims,
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodEdDSA {
				return nil, fmt.Errorf(
					"unexpected signing method: %s",
					token.Method.Alg(),
				)
			}

			publicKey, err := parseEd25519PublicKey(publicKey)
			if err != nil {
				return nil, fmt.Errorf(
					"invalid widget public key: %w",
					err,
				)
			}

			return publicKey, nil
		},
	)

	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"invalid widget code: %v",
			err,
		)
	}

	if !token.Valid {
		return nil, status.Error(
			codes.Unauthenticated,
			"invalid widget code",
		)
	}

	if claims.UserID == 0 {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget code has empty user id",
		)
	}

	if claims.ExamKey == "" {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget code has empty exam key",
		)
	}

	if claims.JTI == "" {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget code has empty jti",
		)
	}

	if !claims.VerifyAudience("marusya-widget", true) {
		return nil, status.Error(
			codes.Unauthenticated,
			"invalid widget code audience",
		)
	}

	if claims.ExpiresAt != nil &&
		!claims.ExpiresAt.After(time.Now()) {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget code expired",
		)
	}

	response := &proto.WidgetCodeData{
		UserId:       claims.UserID,
		ExamKey:      claims.ExamKey,
		Jti:          claims.JTI,
		NeverExpires: claims.NeverExpires,
		AllowedUrls:  claims.AllowedUrls,
	}

	if claims.ExpiresAt != nil {
		response.ExpiresAt = claims.ExpiresAt.Unix()
	}

	return response, nil
}

func (e *Exam) WidgetParseExpiredToken(expiredTokenString string) (*proto.WidgetTokenData, error) {
	if strings.TrimSpace(expiredTokenString) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"empty widget token",
		)
	}

	var claims Claims

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())

	token, err := parser.ParseWithClaims(
		expiredTokenString,
		&claims,
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf(
					"unexpected signing method: %s",
					token.Method.Alg(),
				)
			}

			return e.sessionKey, nil
		},
	)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"invalid expired widget token: %v",
			err,
		)
	}

	if !token.Valid {
		return nil, status.Error(
			codes.Unauthenticated,
			"invalid expired widget token",
		)
	}

	if claims.UserId == 0 {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget token has empty user id",
		)
	}

	if claims.JTI == "" {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget token has empty jti",
		)
	}

	if claims.Origin == "" {
		return nil, status.Error(
			codes.Unauthenticated,
			"widget token has empty origin",
		)
	}

	result := &proto.WidgetTokenData{
		UserId: claims.UserId,
		RespId: claims.ReapId,
		Origin: claims.Origin,
		Jti:    claims.JTI,
	}

	if claims.ExpiresAt != nil {
		result.ExpiredSeconds = claims.ExpiresAt.Unix()
	}

	return result, nil
}
