package exam

import (
	"air_orchestrator/internal/config"
	"air_orchestrator/internal/domain/repository"
	"air_orchestrator/internal/domain/state"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/ikermy/air-logger/v2/pkg/logger"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

type RegData struct {
	created time.Time
	key     string
}

type Resp struct {
	Token  string `json:"token"`
	Permit bool   `json:"permit"`
}

// RedisClient — минимальный интерфейс для работы с Redis.
// Реализуется *redis.Client из internal/infrastructure/redis.
type RedisClient interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Get(ctx context.Context, key string) ([]byte, error)
	Keys(ctx context.Context, pattern string) ([]string, error)
	Del(ctx context.Context, key string) error
	Close() error
}

type Exam struct {
	// sessionKey используется только для подписи токенов JWT.
	// Может ротироваться через CreateNewSessionKey без потери доступа пользователей.
	sessionKey []byte
	// created хранит время создания Exam и так же изменяется при создании нового sessionKey
	created        time.Time
	tokenCache     sync.Map
	regData        map[uint64]*RegData
	emailEncKey    [32]byte    // ключ AES-256-GCM для шифрования email (стабильный: auth.email_key)
	emailHMACKey   [32]byte    // ключ HMAC-SHA256 для детерминированного хэша email (стабильный: auth.email_key)
	totpEncKey     [32]byte    // ключ AES-256-GCM для шифрования TOTP secret (стабильный: auth.email_key)
	masterKeyCache sync.Map    // userId (uint32) → [32]byte — расшифрованный MasterKey в RAM
	appMasterKey   [32]byte    // SHA-256(APP_MASTER_KEY) для шифрования MasterKey в Redis
	redisCli       RedisClient // опциональный Redis-клиент для хранения MasterKey
}

// AssistToken структура для хранения данных в токене jwt
type AssistToken struct {
	jwt.RegisteredClaims
	ReapId uint64 `json:"respId"`
	Assist uint8  `json:"assist"`
}

// AuthToken структура для хранения данных в токене jwt
type AuthToken struct {
	jwt.RegisteredClaims
	RespId uint64 `json:"resp"`
	UserId uint32 `json:"user"`
}

// LandingToken используется только для демо ассистентов
type LandingToken struct {
	Assist uint8
	ReapId uint64
}

// SessionKeyStore — минимальный интерфейс для загрузки/сохранения session key.
// Реализуется *mysql.DB через AppConfigRepository.
type SessionKeyStore interface {
	repository.AppConfig
}

func New() *Exam {
	e := &Exam{
		sessionKey: []byte{},   // заменится через LoadOrInitKey (auth.session)
		created:    time.Now(), // заменится через LoadOrInitKey
		regData:    make(map[uint64]*RegData),
	}
	// emailEncKey, emailHMACKey, totpEncKey будут установлены в LoadOrInitKey
	// из стабильного ключа auth.email_key (НЕ из sessionKey).

	// MasterKey для шифрования MasterKey пользователей в Redis.
	// Загружен в main.go из APP_MASTER_KEY_FILE (fatal при отсутствии).
	e.appMasterKey = sha256.Sum256(state.MasterKey)

	return e
}

// SetRedisClient устанавливает опциональный Redis-клиент для хранения MasterKey.
func (e *Exam) SetRedisClient(cli RedisClient) {
	e.redisCli = cli
}

// createRandomSHA3 создаёт случайный SHA3-256 хэш
func (e *Exam) createRandomSHA3() (string, error) {
	// Создаём массив байт длиной 32 (256 бит)
	key := make([]byte, 32)

	// Заполняем массив случайными байтами
	_, err := rand.Read(key)
	if err != nil {
		return "", fmt.Errorf("ошибка генерации случайного ключа: %w", err)
	}

	// Создаём хэш пароля
	sha := sha3.New256()
	sha.Write(key)

	return fmt.Sprintf("%x", sha.Sum(nil)), nil
}

// SetNewSessionKey генерирует новый session key только в памяти.
// Для персистентного сброса используется CreateNewSessionKey handler —
// он очищает auth.session в БД и перезапускает приложение.
func (e *Exam) SetNewSessionKey() error {
	newKey, err := e.createRandomSHA3()
	if err != nil {
		return fmt.Errorf("ошибка генерации нового sessionKey: %w", err)
	}
	e.sessionKey = []byte(newKey)
	e.created = time.Now()
	return nil
}

// GetTimeCreatedSessionKey GetCreatedSessionKey возвращает время создания текущего sessionKey
func (e *Exam) GetTimeCreatedSessionKey() string {
	return e.created.Format(time.RFC3339)
}

// AddRegUser Генерация уникального ключа для клиента и сохранение его в мапе
func (e *Exam) AddRegUser(respId uint64) (string, error) {
	// Создаём массив байт длиной 32 (256 бит)
	key := make([]byte, 32)

	// Заполняем массив случайными байтами
	_, err := rand.Read(key)
	if err != nil {
		return "", fmt.Errorf("ошибка генерации ключа: %w", err)
	}

	// Конвертируем байты в шестнадцатеричную строку
	hexKey := hex.EncodeToString(key)

	now := time.Now()
	e.regData[respId] = &RegData{
		created: now,
		key:     hexKey,
	}

	return hexKey, err
}

// GenerateAccessToken Генерация короткого и долгого access токенов для пользователя
func (e *Exam) GenerateAccessToken(userId uint32, respId uint64) (sta, lta string, err error) {
	// Создаем короткий токен (с обычным временем жизни)
	shortClaims := AuthToken{
		RespId: respId,
		UserId: userId,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(config.AccessTokenTTL)), // Стандартное время жизни
			IssuedAt:  jwt.NewNumericDate(time.Now()),                            // Время создания токена
		},
	}

	// Создаем долгий токен (в 336 раз дольше)
	longClaims := AuthToken{
		RespId: respId,
		UserId: userId,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(config.RefreshTokenTTL)), // Увеличенное время жизни
			IssuedAt:  jwt.NewNumericDate(time.Now()),                             // Время создания токена
		},
	}

	// Подпись короткого токена
	shortToken := jwt.NewWithClaims(jwt.SigningMethodHS256, shortClaims)
	sta, err = shortToken.SignedString(e.sessionKey)
	if err != nil {
		logger.Error("ошибка подписи короткого токена: %v", err, userId)
		return "", "", err
	}

	// Подпись долгого токена
	longToken := jwt.NewWithClaims(jwt.SigningMethodHS256, longClaims)
	lta, err = longToken.SignedString(e.sessionKey)
	if err != nil {
		logger.Error("ошибка подписи долгого токена: %v", err, userId)
		return "", "", err
	}

	return sta, lta, nil
}

// VerifyAccessToken проверяет любой JWT токен доступа (STA или LTA) и возвращает ID пользователя
func (e *Exam) VerifyAccessToken(tokenString string) (uint32, uint64, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("неподдерживаемый метод подписи: %v", token.Header["alg"])
		}
		return e.sessionKey, nil
	})

	if err != nil {
		return 0, 0, fmt.Errorf("ошибка при парсинге токена: %w", err)
	}

	if !token.Valid {
		return 0, 0, fmt.Errorf("невалидный токен")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, 0, fmt.Errorf("невозможно получить claims из токена")
	}

	// Extract userId (as uint32)
	var userId uint32
	if userFloat, ok := claims["user"].(float64); ok {
		userId = uint32(userFloat) // Safe conversion with potential data loss
	} else {
		return 0, 0, fmt.Errorf("невалидный формат userId в токене")
	}

	// Extract respId (as uint64)
	var respId uint64
	if respFloat, ok := claims["resp"].(float64); ok {
		respId = uint64(respFloat)
	} else {
		return 0, 0, fmt.Errorf("невалидный формат respId в токене")
	}

	return userId, respId, nil
}

// BlacklistToken добавляет токен в Redis блэклист с TTL.
// В значении сохраняем время добавления для реализации Grace Period.
func (e *Exam) BlacklistToken(ctx context.Context, token string, expiration time.Duration) error {
	if e.redisCli == nil {
		return nil // Redis не настроен, пропускаем блэклистинг
	}
	now := time.Now().Unix()
	key := "tokens_blacklist:" + token
	return e.redisCli.Set(ctx, key, []byte(strconv.FormatInt(now, 10)), expiration)
}

// IsBlacklisted проверяет, находится ли токен в блэклисте.
// Реализует 10-секундный Grace Period: если токен добавлен менее 10 секунд назад,
// он НЕ считается заблокированным (для поддержки параллельных запросов).
func (e *Exam) IsBlacklisted(ctx context.Context, token string) (bool, error) {
	if e.redisCli == nil {
		return false, nil
	}
	key := "tokens_blacklist:" + token
	val, err := e.redisCli.Get(ctx, key)
	if err != nil {
		return false, nil // Токена нет в Redis или ошибка
	}

	timestamp, err := strconv.ParseInt(string(val), 10, 64)
	if err != nil {
		return true, nil // Битые данные — считаем заблокированным от греха подальше
	}

	// 10 секунд Grace Period
	if time.Since(time.Unix(timestamp, 0)) < 10*time.Second {
		return false, nil
	}

	return true, nil
}

// ParseAuthToken парсит аутентификационный токен jwt
func (e *Exam) ParseAuthToken(tokenString *string) (*uint32, *uint64, error) {
	token, err := jwt.ParseWithClaims(*tokenString, &AuthToken{},
		func(token *jwt.Token) (any, error) {
			return e.sessionKey, nil
		})
	if err != nil {
		return nil, nil, err
	}

	if claims, ok := token.Claims.(*AuthToken); ok && token.Valid {
		return &claims.UserId, &claims.RespId, nil
	}

	return nil, nil, fmt.Errorf("invalid token")
}

// DecryptPassword расшифровывает пароль, проверяет срок действия ключа и удаляет запись
func (e *Exam) DecryptPassword(respId uint64, encryptedPassword string) (string, error) {
	// Получаем данные регистрации
	regData, exists := e.regData[respId]

	// Отложенное удаление записи в любом случае
	defer delete(e.regData, respId)

	if !exists {
		return "", fmt.Errorf("регистрационные данные не найдены для respId %d", respId)
	}

	// Проверяем, что с момента создания прошло менее 30 секунд
	if time.Since(regData.created) > config.RegKeyTTL*time.Second {
		return "", fmt.Errorf("истек срок действия регистрационного ключа")
	}

	// Расшифровка пароля
	return decryptAESPassword(encryptedPassword, regData.key)
}

// decryptAESPassword расшифровывает пароль
func decryptAESPassword(ciphertext, passphrase string) (string, error) {
	// Декодирование из base64
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("ошибка декодирования base64: %w", err)
	}

	// Проверка на минимальную длину
	if len(ciphertextBytes) < 16 {
		return "", fmt.Errorf("недопустимый формат шифротекста")
	}

	// Извлечение соли (должен быть префикс "Salted__" + 8 байт соли)
	salt := ciphertextBytes[8:16]
	cipherdata := ciphertextBytes[16:]

	// Генерация ключа и IV используя правильный алгоритм (как в CryptoJS)
	key, iv := deriveKeyAndIV([]byte(passphrase), salt)

	// Создание блока шифрования
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// Режим CBC для расшифровки
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(cipherdata))
	mode.CryptBlocks(plaintext, cipherdata)

	// Удаление PKCS#7 padding
	paddingLen := int(plaintext[len(plaintext)-1])
	if paddingLen > 0 && paddingLen <= aes.BlockSize {
		plaintext = plaintext[:len(plaintext)-paddingLen]
	}

	return string(plaintext), nil
}

// Генерация ключа и IV из пароля и соли
func deriveKeyAndIV(password, salt []byte) ([]byte, []byte) {
	// Нам нужно 48 байт (32 для ключа, 16 для IV)
	var result []byte

	// Данные для первой итерации MD5
	data := append(password, salt...)

	// Генерируем хеши MD5, пока не получим достаточно данных
	for len(result) < 48 {
		hash := md5.Sum(data)
		result = append(result, hash[:]...)

		// Для следующей итерации - хеш + пароль + соль
		data = append(hash[:], append(password, salt...)...)
	}

	// Теперь у нас точно есть минимум 48 байт
	key := result[:32]
	iv := result[32:48]

	return key, iv
}

// CreateSHA3 создаёт хэш SHA3-256 от строки (для записи хэша в БД)
// Устарело: используйте HashPassword для новых пользователей
func (e *Exam) createSHA3(pass string) string {
	// Создаём хэш пароля
	sha := sha3.New256()
	sha.Write([]byte(pass))

	return fmt.Sprintf("%x", sha.Sum(nil))
}

// HashPassword хэширует пароль с использованием bcrypt (cost=12)
// Возвращает строку вида "$2a$12$..."
func (e *Exam) HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	if err != nil {
		return "", fmt.Errorf("ошибка хэширования пароля: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword проверяет пароль против хэша.
// Поддерживает bcrypt (новый формат) и SHA3-256 (устаревший формат).
func (e *Exam) VerifyPassword(storedHash, plain string) bool {
	if strings.HasPrefix(storedHash, "$2a$") || strings.HasPrefix(storedHash, "$2b$") {
		return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(plain)) == nil
	}
	// Устаревший формат SHA3-256
	return storedHash == e.createSHA3(plain)
}

// EncryptEmail шифрует email с использованием AES-256-GCM.
// Результат хранится с префиксом "$enc$" для различения от plaintext.
func (e *Exam) EncryptEmail(email string) (string, error) {
	block, err := aes.NewCipher(e.emailEncKey[:])
	if err != nil {
		return "", fmt.Errorf("ошибка создания шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("ошибка создания GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("ошибка генерации nonce: %w", err)
	}
	// Нормализуем email к нижнему регистру перед шифрованием
	ciphertext := gcm.Seal(nonce, nonce, []byte(strings.ToLower(email)), nil)
	return "$enc$" + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptEmail расшифровывает email.
// Если строка не начинается с "$enc$" — возвращает как есть (plaintext).
func (e *Exam) decryptEmail(raw string) (string, error) {
	if !strings.HasPrefix(raw, "$enc$") {
		return raw, nil
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "$enc$"))
	if err != nil {
		return "", fmt.Errorf("ошибка декодирования base64: %w", err)
	}
	block, err := aes.NewCipher(e.emailEncKey[:])
	if err != nil {
		return "", fmt.Errorf("ошибка создания шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("ошибка создания GCM: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("некорректные зашифрованные данные")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("ошибка расшифровки email: %w", err)
	}
	return string(plaintext), nil
}

// DecryptEmailIfNeeded расшифровывает email если он зашифрован.
// Псевдоним для DecryptEmail для семантической ясности в хендлерах.
func (e *Exam) DecryptEmailIfNeeded(raw string) (string, error) {
	return e.decryptEmail(raw)
}

// EmailHMAC вычисляет HMAC-SHA256 от нормализованного email.
// Используется как детерминированный ключ поиска и уникальности в БД.
// Возвращает hex-строку из 64 символов.
func (e *Exam) EmailHMAC(email string) string {
	mac := hmac.New(sha256.New, e.emailHMACKey[:])
	mac.Write([]byte(strings.ToLower(email)))
	return hex.EncodeToString(mac.Sum(nil))
}

// DecryptEmailInJSON расшифровывает поле email в JSON по указанному пути.
// path — точечная нотация, например "Email" или "email.data".
func (e *Exam) DecryptEmailInJSON(raw []byte, path string) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	parts := strings.SplitN(path, ".", 2)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw, nil
	}
	if len(parts) == 1 {
		// Финальный уровень — расшифровываем строку
		val, ok := obj[parts[0]]
		if !ok {
			return raw, nil
		}
		var encStr string
		if err := json.Unmarshal(val, &encStr); err != nil {
			return raw, nil
		}
		plain, err := e.decryptEmail(encStr)
		if err != nil {
			return raw, nil
		}
		plainJSON, _ := json.Marshal(plain)
		obj[parts[0]] = plainJSON
	} else {
		// Вложенный уровень — рекурсивно обрабатываем вложенный объект
		nested, ok := obj[parts[0]]
		if !ok {
			return raw, nil
		}
		updated, err := e.DecryptEmailInJSON(nested, parts[1])
		if err != nil {
			return raw, nil
		}
		obj[parts[0]] = updated
	}
	result, err := json.Marshal(obj)
	if err != nil {
		return raw, nil
	}
	return result, nil
}

// GetMailConfirmationToken GenerateConfirmationToken генерирует токен для подтверждения email
func (e *Exam) GetMailConfirmationToken(userId uint32, email string) (string, error) {
	// Создаем структуру данных для JWT токена
	claims := jwt.MapClaims{
		"email": email,
		"user":  userId,
		"exp":   time.Now().Add(config.MailTokenTTL * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"jti":   fmt.Sprintf("%s-%d", email, time.Now().UnixNano()),
	}

	// Создаем токен с подписью
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(e.sessionKey)
	if err != nil {
		logger.Error("ошибка подписи email confirmation токена: %v", err, userId)
		return "", err
	}

	return signedToken, nil
}

// ParseMailConfirmationToken ParseConfirmationToken проверяет токен подтверждения email и возвращает email
func (e *Exam) ParseMailConfirmationToken(tokenString string) (uint32, string, error) {
	// Парсим токен
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		// Проверяем, что алгоритм подписи соответствует ожидаемому
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("неожиданный метод подписи: %v", token.Header["alg"])
		}
		// Возвращаем ключ для проверки подписи
		return e.sessionKey, nil
	})

	if err != nil {
		logger.Error("ошибка при проверке токена подтверждения email: %v", err)
		return 0, "", err
	}

	// Проверяем валидность токена
	if !token.Valid {
		return 0, "", fmt.Errorf("недействительный токен")
	}

	// Получаем claims из токена
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, "", fmt.Errorf("не удалось получить claims из токена")
	}

	// Проверяем наличие email в токене
	email, ok := claims["email"].(string)
	if !ok || email == "" {
		return 0, "", fmt.Errorf("токен не содержит email")
	}

	// Проверяем наличие userId в токене
	userIdFloat, ok := claims["user"].(float64)
	if !ok {
		return 0, "", fmt.Errorf("токен не содержит userId")
	}

	userId := uint32(userIdFloat)
	// JWT автоматически проверяет истечение срока действия (exp)

	return userId, email, nil
}

// ============================================================================
// TOTP (Google Authenticator)
// ============================================================================

// GenerateTOTPSecret генерирует новый TOTP secret и URI для QR-кода.
// accountName — обычно email пользователя.
func (e *Exam) GenerateTOTPSecret(accountName string) (secret, uri string, err error) {
	key, genErr := totp.Generate(totp.GenerateOpts{
		Issuer:      state.TOTPName,
		AccountName: accountName,
		Period:      30,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if genErr != nil {
		return "", "", fmt.Errorf("ошибка генерации TOTP: %w", genErr)
	}
	return key.Secret(), key.URL(), nil
}

// EncryptTOTPSecret шифрует TOTP secret с помощью AES-256-GCM.
func (e *Exam) EncryptTOTPSecret(secret string) (string, error) {
	block, err := aes.NewCipher(e.totpEncKey[:])
	if err != nil {
		return "", fmt.Errorf("ошибка создания шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("ошибка создания GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("ошибка генерации nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(secret), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptTOTPSecret расшифровывает TOTP secret.
func (e *Exam) decryptTOTPSecret(encrypted string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("ошибка декодирования base64: %w", err)
	}
	block, err := aes.NewCipher(e.totpEncKey[:])
	if err != nil {
		return "", fmt.Errorf("ошибка создания шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("ошибка создания GCM: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("некорректные зашифрованные данные")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("ошибка расшифровки TOTP secret: %w", err)
	}
	return string(plaintext), nil
}

// ValidateTOTPCode проверяет 6-значный TOTP-код против зашифрованного secret.
// Допускает ±1 период (30 сек) для компенсации расхождения часов.
func (e *Exam) ValidateTOTPCode(encryptedSecret, code string) bool {
	secret, err := e.decryptTOTPSecret(encryptedSecret)
	if err != nil {
		return false
	}
	return totp.Validate(code, secret)
}

// ============================================================================
// MasterKey — wrap паролем через PBKDF2+AES-256-GCM, хранится в RAM после логина
// ============================================================================

const masterKeyPBKDF2Iter = 100_000

// GenerateAndWrapMasterKey генерирует новый 32-байтовый MasterKey,
// оборачивает его паролем (PBKDF2 + AES-256-GCM) и кладёт в cache.
// rawB64 возвращается клиенту ОДИН РАЗ — пользователь обязан его сохранить.
func (e *Exam) GenerateAndWrapMasterKey(userId uint32, password string) (rawB64, encMK, wrapSalt string, err error) {
	rawKey := make([]byte, 32)
	if _, err = rand.Read(rawKey); err != nil {
		return "", "", "", fmt.Errorf("ошибка генерации MasterKey: %w", err)
	}

	encMK, wrapSalt, err = e.wrapMasterKeyBytes(rawKey, password)
	if err != nil {
		return "", "", "", err
	}

	rawB64 = base64.StdEncoding.EncodeToString(rawKey)

	var cached [32]byte
	copy(cached[:], rawKey)
	e.masterKeyCache.Store(userId, cached)

	// Сохраняем зашифрованный MasterKey в Redis (если настроен)
	if err = e.saveMasterKeyToRedis(userId, cached); err != nil {
		logger.Warn("Ошибка сохранения ключа в redis %v", err, userId)
	}

	return rawB64, encMK, wrapSalt, nil
}

// WrapMasterKey оборачивает уже известный raw MasterKey (base64) с паролем.
// Используется при смене пароля (RewrapMasterKey) и сбросе пароля (ResetPass).
func (e *Exam) WrapMasterKey(rawB64, password string) (encMK, wrapSalt string, err error) {
	rawKey, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return "", "", fmt.Errorf("ошибка декодирования raw MasterKey: %w", err)
	}
	if len(rawKey) != 32 {
		return "", "", fmt.Errorf("некорректная длина MasterKey: ожидается 32 байта, получено %d", len(rawKey))
	}
	return e.wrapMasterKeyBytes(rawKey, password)
}

// TODO cделать механизм уведомления сервисов о появлении MasterKey в cache
// LoadMasterKey расшифровывает MasterKey из БД и кладёт в cache.
// Вызывается после успешной авторизации (Autentificate / AuthTOTP).
func (e *Exam) LoadMasterKey(userId uint32, password, encMK, wrapSaltB64 string) error {
	wrapSaltBytes, err := base64.StdEncoding.DecodeString(wrapSaltB64)
	if err != nil {
		return fmt.Errorf("ошибка декодирования wrapSalt: %w", err)
	}

	// OLD golang.org/x/crypto/pbkdf2
	//wrapKey := pbkdf2.Key([]byte(password), wrapSaltBytes, masterKeyPBKDF2Iter, 32, sha256.New)

	wrapKey, err := pbkdf2.Key(sha256.New, password, wrapSaltBytes, masterKeyPBKDF2Iter, 32)
	if err != nil {
		return fmt.Errorf("ошибка генерации wrapKey через pbkdf2: %w", err)
	}

	data, err := base64.StdEncoding.DecodeString(encMK)
	if err != nil {
		return fmt.Errorf("ошибка декодирования encMK: %w", err)
	}

	block, err := aes.NewCipher(wrapKey)
	if err != nil {
		return fmt.Errorf("ошибка создания шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("ошибка создания GCM: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return fmt.Errorf("некорректные данные encMK: слишком короткие")
	}

	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	rawKey, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("ошибка расшифровки MasterKey (неверный пароль?): %w", err)
	}
	if len(rawKey) != 32 {
		return fmt.Errorf("некорректная длина расшифрованного MasterKey")
	}

	var cached [32]byte
	copy(cached[:], rawKey)
	e.masterKeyCache.Store(userId, cached)

	// Сохраняем зашифрованный MasterKey в Redis (если настроен)
	if err = e.saveMasterKeyToRedis(userId, cached); err != nil {
		logger.Warn("Ошибка сохранения ключа в redis %v", err, userId)
	}

	return nil
}

// GetMasterKey возвращает MasterKey из cache.
// Если (false) — ключ отсутствует (сервер перезапустился / пользователь ещё не логинился):
// вызывающий код работает без шифрования данных.
func (e *Exam) GetMasterKey(userId uint32) ([32]byte, bool) {
	val, ok := e.masterKeyCache.Load(userId)
	if !ok {
		return [32]byte{}, false
	}
	return val.([32]byte), true
}

// ─── Шифрование/дешифрование MasterKey для Redis ────────────────────────────

// encryptMasterKeyForRedis шифрует MasterKey ключом appMasterKey (APP_MASTER_KEY)
// для безопасного хранения в Redis. Формат: base64(nonce || ciphertext).
func (e *Exam) encryptMasterKeyForRedis(mk [32]byte) (string, error) {
	if isZeroKey(e.appMasterKey) {
		return "", fmt.Errorf("appMasterKey не задан — шифрование невозможно")
	}

	block, err := aes.NewCipher(e.appMasterKey[:])
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, mk[:], nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptMasterKeyFromRedis расшифровывает MasterKey из Redis.
func (e *Exam) decryptMasterKeyFromRedis(encB64 string) ([32]byte, error) {
	if isZeroKey(e.appMasterKey) {
		return [32]byte{}, fmt.Errorf("appMasterKey не задан — дешифрование невозможно")
	}

	data, err := base64.StdEncoding.DecodeString(encB64)
	if err != nil {
		return [32]byte{}, fmt.Errorf("base64 decode: %w", err)
	}

	block, err := aes.NewCipher(e.appMasterKey[:])
	if err != nil {
		return [32]byte{}, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return [32]byte{}, fmt.Errorf("cipher.NewGCM: %w", err)
	}

	if len(data) < gcm.NonceSize() {
		return [32]byte{}, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return [32]byte{}, fmt.Errorf("gcm.Open: %w", err)
	}

	var mk [32]byte
	copy(mk[:], plaintext)
	return mk, nil
}

// saveMasterKeyToRedis шифрует MasterKey и сохраняет в Redis с TTL.
func (e *Exam) saveMasterKeyToRedis(userId uint32, mk [32]byte) error {
	if e.redisCli == nil {
		return fmt.Errorf("RedisClient не инициирован")
	}

	encB64, err := e.encryptMasterKeyForRedis(mk)
	if err != nil {
		return fmt.Errorf("MasterKey: ошибка шифрования для Redis: %v", err)
	}

	key := fmt.Sprintf("masterkey:%d", userId)
	if err = e.redisCli.Set(context.Background(), key, []byte(encB64), config.MasterKeyRedisTTL); err != nil {
		return fmt.Errorf("MasterKey: ошибка сохранения в Redis: %v", err)
	}

	return nil
}

func (e *Exam) RefreshMasterKeyTTL(userId uint32) error {
	if e.redisCli == nil {
		return fmt.Errorf("RedisClient не инициирован")
	}

	key := fmt.Sprintf("masterkey:%d", userId)
	// обновляем TTL
	ok, err := e.redisCli.Expire(context.Background(), key, config.MasterKeyRedisTTL)
	if err != nil {
		return fmt.Errorf("MasterKey: ошибка обновления TTL: %v", err)
	}

	// Возвращаю как ошибку т.к. в RefreshToken всё равно ничего не сделать
	// без пароля пользователя не восстановить UserMasterKey
	if !ok {
		return fmt.Errorf("MasterKey: ключ не найден в Redis")
	}

	return nil
}

// LoadAllMasterKeysFromRedis загружает все MasterKey из Redis в masterKeyCache.
// Вызывается при старте приложения после инициализации Redis-клиента.
func (e *Exam) LoadAllMasterKeysFromRedis(ctx context.Context) error {
	if e.redisCli == nil {
		return nil
	}

	keys, err := e.redisCli.Keys(ctx, "masterkey:*")
	if err != nil {
		return fmt.Errorf("Keys masterkey:*: %w", err)
	}

	var loaded int
	for _, key := range keys {
		// Извлекаем userId из ключа "masterkey:{userId}"
		idStr := strings.TrimPrefix(key, "masterkey:")
		userID, parseErr := strconv.ParseUint(idStr, 10, 32)
		if parseErr != nil {
			logger.Warn("MasterKey: некорректный ключ Redis: %s", key)
			continue
		}

		data, getErr := e.redisCli.Get(ctx, key)
		if getErr != nil {
			logger.Warn("MasterKey: ошибка чтения из Redis: %v", getErr, uint32(userID))
			continue
		}

		mk, decErr := e.decryptMasterKeyFromRedis(string(data))
		if decErr != nil {
			logger.Warn("MasterKey: ошибка расшифровки из Redis: %v", decErr, uint32(userID))
			continue
		}

		e.masterKeyCache.Store(uint32(userID), mk)
		loaded++
	}

	logger.Info("MasterKey: загружено %d ключей из Redis", loaded)
	return nil
}

// isZeroKey проверяет, что [32]byte массив полностью нулевой.
func isZeroKey(key [32]byte) bool {
	for _, b := range key {
		if b != 0 {
			return false
		}
	}
	return true
}

// wrapMasterKeyBytes — внутренний хелпер: PBKDF2(password, salt) + AES-256-GCM encrypt rawKey.
func (e *Exam) wrapMasterKeyBytes(rawKey []byte, password string) (encMK, wrapSaltB64 string, err error) {
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return "", "", fmt.Errorf("ошибка генерации salt: %w", err)
	}

	// OLD golang.org/x/crypto/pbkdf2
	// wrapKey := pbkdf2.Key([]byte(password), salt, masterKeyPBKDF2Iter, 32, sha256.New)

	wrapKey, err := pbkdf2.Key(sha256.New, password, salt, masterKeyPBKDF2Iter, 32)
	if err != nil {
		return "", "", fmt.Errorf("ошибка генерации wrapKey через pbkdf2: %w", err)
	}

	block, err := aes.NewCipher(wrapKey)
	if err != nil {
		return "", "", fmt.Errorf("ошибка создания шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("ошибка создания GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", "", fmt.Errorf("ошибка генерации nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, rawKey, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), base64.StdEncoding.EncodeToString(salt), nil
}

// LoadOrInitKey вызывается один раз при старте из app.New().
// Управляет ДВУМЯ независимыми ключами:
//   - auth.session   → JWT-ключ (может ротироваться через CreateNewSessionKey)
//   - auth.email_key → стабильный ключ email HMAC/AES (НИКОГДА не сбрасывается при ротации JWT)
//
// Разделение гарантирует: ротация JWT-ключа не ломает доступ мигрированных пользователей.
func (e *Exam) LoadOrInitKey(ctx context.Context, store SessionKeyStore) error {
	// ── 1. JWT session key ──────────────────────────────────────────────────
	jwtKey, err := store.GetAppConfig(ctx, "auth.session")
	if err != nil {
		return fmt.Errorf("LoadOrInitKey: ошибка чтения JWT ключа: %w", err)
	}

	if jwtKey != "" {
		e.sessionKey = []byte(jwtKey)
		if created, _ := store.GetAppConfig(ctx, "auth.created"); created != "" {
			if t, parseErr := time.Parse(time.RFC3339, created); parseErr == nil {
				e.created = t
			}
		}
		logger.Info("Exam: JWT session key загружен из БД")
	} else {
		// DB пустая — ключ был сброшен через CreateNewSessionKey или первый запуск
		newKey, genErr := e.createRandomSHA3()
		if genErr != nil {
			return fmt.Errorf("LoadOrInitKey: ошибка генерации JWT ключа: %w", genErr)
		}
		createdTime := time.Now()
		if saveErr := store.SetAppConfig(ctx, "auth.session", newKey); saveErr != nil {
			return fmt.Errorf("LoadOrInitKey: ошибка сохранения JWT ключа: %w", saveErr)
		}
		if saveErr := store.SetAppConfig(ctx, "auth.created", createdTime.Format(time.RFC3339)); saveErr != nil {
			return fmt.Errorf("LoadOrInitKey: ошибка сохранения даты: %w", saveErr)
		}
		e.sessionKey = []byte(newKey)
		e.created = createdTime
		logger.Info("Exam: JWT session key сгенерирован (первый запуск или после сброса)")
	}

	// ── 2. Stable email key ─────────────────────────────────────────────────
	if err := e.loadOrInitEmailKey(ctx, store); err != nil {
		return fmt.Errorf("LoadOrInitKey: ошибка инициализации email ключа: %w", err)
	}

	return nil
}

// loadOrInitEmailKey загружает или инициализирует стабильный ключ шифрования email.
// Порядок приоритетов при первой инициализации: SESSION_KEY_LEGACY env → новый случайный.
// SESSION_KEY_LEGACY указывает на ключ из cfg.env, которым были мигрированы существующие
// пользователи — без него их EmailHash станет недоступен.
// Этот ключ сохраняется как auth.email_key и используется для всех новых запусков.
func (e *Exam) loadOrInitEmailKey(ctx context.Context, store SessionKeyStore) error {
	emailKey, err := store.GetAppConfig(ctx, "auth.email_key")
	if err != nil {
		return fmt.Errorf("ошибка чтения email ключа: %w", err)
	}

	if emailKey == "" {
		// Первая инициализация: предпочитаем совместимый ключ из cfg.env
		if envKey := os.Getenv("SESSION_KEY_LEGACY"); envKey != "" {
			emailKey = envKey
			logger.Info("Exam: email ключ инициализирован из SESSION_KEY_LEGACY (backward compat)")
		} else {
			newKey, genErr := e.createRandomSHA3()
			if genErr != nil {
				return fmt.Errorf("ошибка генерации email ключа: %w", genErr)
			}
			emailKey = newKey
			logger.Info("Exam: email ключ сгенерирован (первый запуск)")
		}
		// Сохраняем в DB — после этого env var больше не нужен
		if saveErr := store.SetAppConfig(ctx, "auth.email_key", emailKey); saveErr != nil {
			return fmt.Errorf("ошибка сохранения email ключа: %w", saveErr)
		}
	} else {
		logger.Info("Exam: email ключ загружен из БД")
	}

	emailKeyBytes := []byte(emailKey)
	e.emailEncKey = sha256.Sum256(append([]byte("emailenc:"), emailKeyBytes...))
	e.emailHMACKey = sha256.Sum256(append([]byte("emailhmac:"), emailKeyBytes...))
	e.totpEncKey = sha256.Sum256(append([]byte("totpenc:"), emailKeyBytes...))

	return nil
}
