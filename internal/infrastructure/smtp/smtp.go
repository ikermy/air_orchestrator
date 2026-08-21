package smtp

import (
	"air_orchestrator/internal/config"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ikermy/air_common/pkg/endpoint"
	"github.com/ikermy/air_common/pkg/mode"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// store — минимальный интерфейс для чтения настроек из app_config.
// Реализуется *mysql.DB.
type store interface {
	GetAppConfig(ctx context.Context, key string) (string, error)
}

// end — минимальный интерфейс только методы получения перевода
type end interface {
	TranslateMessageWithUserID(userID uint32, message string) string
	TranslateMessageWithLang(lang, message string) string
}

type SMTP struct {
	host   string
	port   string
	mail   string
	pass   string
	end    end
	ctx    context.Context
	cancel context.CancelFunc
}

func encodeSubject(subject string) string {
	return mime.BEncoding.Encode("UTF-8", fmt.Sprintf("MarusiaAI %s", subject))
}

// New создаёт SMTP-клиент, загружая параметры из app_config БД (smtp.*).
// Ключи: smtp.host, smtp.port, smtp.mail, smtp.pass.
func New(parent context.Context, store store, end *endpoint.Endpoint) *SMTP {
	ctx, cancel := context.WithCancel(parent)

	get := func(key string) string {
		v, _ := store.GetAppConfig(ctx, key)
		return v
	}

	return &SMTP{
		end:    end,
		ctx:    ctx,
		cancel: cancel,
		host:   get("smtp.host"),
		port:   get("smtp.port"),
		mail:   get("smtp.mail"),
		pass:   get("smtp.pass"),
	}
}

// extractDomain извлекает домен из email адреса
func extractDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return "localhost"
}

//func (s *SMTP) TLSSendConfirmMail(recepient, link string) error {
//	// Получатель и текст письма
//	to := []string{recepient}
//
//	// Формирование HTML-письма с ссылкой для подтверждения
//	confirmLink := fmt.Sprintf("%s/confirm?key=%s", mode.RealHost, link)
//
//	// Обязательные заголовки для предотвращения попадания в спам
//	domain := extractDomain(s.mail)
//	from := fmt.Sprintf("From: MarusiaAI <%s>\r\n", s.mail)
//	toHeader := fmt.Sprintf("To: %s\r\n", recepient)
//	replyTo := fmt.Sprintf("Reply-To: %s\r\n", s.mail)
//	date := fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z))
//	messageID := fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.New().String(), domain)
//	subject := "Subject: =?UTF-8?B?TWFydXNpYUFJIC0g0L/QvtC00YLQstC10YDQttC00LXQvdC40LUg0LDQtNGA0LXRgdCwINC/0L7Rh9GC0Ys=?=\r\n"
//	xMailer := "X-Mailer: MarusiaAI Mailer v1.0\r\n"
//	mime := "MIME-Version: 1.0\r\n"
//	contentType := "Content-Type: text/html; charset=utf-8\r\n"
//
//	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
//<html lang="ru">
//<head><meta charset="UTF-8"><title>Подтверждение регистрации</title></head>
//<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
//    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
//        <h2 style="color: #4a90e2;">Добро пожаловать в MarusiaAI!</h2>
//        <p>Вы успешно зарегистрировались на сайте MarusiaAI.</p>
//        <p>Для подтверждения вашего адреса электронной почты, пожалуйста, перейдите по следующей ссылке:</p>
//        <p style="text-align: center; margin: 30px 0;">
//            <a href="%s" style="display: inline-block; padding: 12px 24px; background-color: #4a90e2; color: #ffffff; text-decoration: none; border-radius: 4px;">Подтвердить адрес электронной почты</a>
//        </p>
//        <p style="color: #666; font-size: 14px;">Если вы не запрашивали подтверждение, просто проигнорируйте это письмо.</p>
//        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
//        <p style="color: #999; font-size: 12px;">С уважением,<br>Команда MarusiaAI</p>
//    </div>
//</body>
//</html>`, confirmLink)
//
//	message := []byte(from + toHeader + replyTo + date + messageID + subject + xMailer + mime + contentType + "\r\n" + htmlBody)
//
//	// Настройка TLS конфигурации
//	tlsConfig := &tls.Config{
//		ServerName:         s.host,
//		InsecureSkipVerify: false,
//	}
//
//	// Создание защищенного соединения
//	conn, err := tls.Dial("tcp", s.host+":"+s.port, tlsConfig)
//	if err != nil {
//		return fmt.Errorf("ошибка подключения к серверу: %w", err)
//	}
//
//	// Создание клиента SMTP поверх TLS соединения
//	client, err := smtp.NewClient(conn, s.host)
//	if err != nil {
//		return fmt.Errorf("ошибка создания SMTP клиента: %w", err)
//	}
//	defer func(client *smtp.Client) {
//		err := client.Close()
//		if err != nil {
//			logger.Error("ошибка закрытия SMTP клиента: %v\n", err)
//		}
//	}(client)
//
//	// Аутентификация
//	auth := smtp.PlainAuth("", s.mail, s.pass, s.host)
//	if err := client.Auth(auth); err != nil {
//		return fmt.Errorf("ошибка аутентификации: %w", err)
//	}
//
//	// Установка отправителя и получателя
//	if err := client.Mail(s.mail); err != nil {
//		return fmt.Errorf("ошибка указания отправителя: %w", err)
//	}
//
//	for _, recipient := range to {
//		if err := client.Rcpt(recipient); err != nil {
//			return fmt.Errorf("ошибка указания получателя: %w", err)
//		}
//	}
//
//	// Отправка сообщения
//	w, err := client.Data()
//	if err != nil {
//		return fmt.Errorf("ошибка подготовки данных: %w", err)
//	}
//
//	_, err = w.Write(message)
//	if err != nil {
//		return fmt.Errorf("ошибка записи сообщения: %w", err)
//	}
//
//	err = w.Close()
//	if err != nil {
//		return fmt.Errorf("ошибка завершения отправки: %w", err)
//	}
//
//	return nil
//}

func (s *SMTP) SendConfirmMail(lang, recipient, token string) error {
	confirmReg := s.end.TranslateMessageWithLang(lang, "confirm.registration")
	welcome := s.end.TranslateMessageWithLang(lang, "welcome")
	forConfirmReg := s.end.TranslateMessageWithLang(lang, "for.confirm.registration")
	ifYouHaventReq := s.end.TranslateMessageWithLang(lang, "if.you.haven.t.requested")
	sincerely := s.end.TranslateMessageWithLang(lang, "sincerely.marusia.team")

	to := []string{recipient}
	confirmLink := fmt.Sprintf("https://%s/confirm?key=%s", mode.GetRealHost(), token)

	// Обязательные заголовки для предотвращения попадания в спам
	domain := extractDomain(s.mail)
	from := fmt.Sprintf("From: MarusiaAI <%s>\r\n", s.mail)
	toHeader := fmt.Sprintf("To: %s\r\n", recipient)
	replyTo := fmt.Sprintf("Reply-To: %s\r\n", s.mail)
	date := fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	messageID := fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.New().String(), domain)
	subject := fmt.Sprintf("Subject: %s\r\n", encodeSubject(confirmReg))
	xMailer := "X-Mailer: MarusiaAI Mailer v1.0\r\n"
	mime := "MIME-Version: 1.0\r\n"
	contentType := "Content-Type: text/html; charset=utf-8\r\n"

	//	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
	//<html lang="ru">
	//<head><meta charset="UTF-8"><title>Подтверждение регистрации</title></head>
	//<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
	//    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
	//        <h2 style="color: #4a90e2;">Добро пожаловать в MarusiaAI!</h2>
	//        <p>Вы успешно зарегистрировались на сайте MarusiaAI.</p>
	//        <p>Для подтверждения вашего адреса электронной почты, пожалуйста, перейдите по следующей ссылке:</p>
	//        <p style="text-align: center; margin: 30px 0;">
	//            <a href="%s" style="display: inline-block; padding: 12px 24px; background-color: #4a90e2; color: #ffffff; text-decoration: none; border-radius: 4px;">Подтвердить адрес электронной почты</a>
	//        </p>
	//        <p style="color: #666; font-size: 14px;">Если вы не запрашивали подтверждение, просто проигнорируйте это письмо.</p>
	//        <p style="color: #666; font-size: 14px;">Ссылка действительна в течение ограниченного времени.</p>
	//        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
	//        <p style="color: #999; font-size: 12px;">С уважением,<br>Команда MarusiaAI</p>
	//    </div>
	//</body>
	//</html>`, confirmLink)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="UTF-8"><title>%s</title></head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #4a90e2;">%s</h2>
        %s
        <p style="text-align: center; margin: 30px 0;">
            <a href="%s" style="display: inline-block; padding: 12px 24px; background-color: #4a90e2; color: #ffffff; text-decoration: none; border-radius: 4px;">Подтвердить адрес электронной почты</a>
        </p>
        %s
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #999; font-size: 12px;">%s</p>
    </div>
</body>
</html>`, confirmReg, welcome, forConfirmReg, confirmLink, ifYouHaventReq, sincerely)

	// Правильный порядок заголовков
	message := []byte(from + toHeader + replyTo + date + messageID + subject + xMailer + mime + contentType + "\r\n" + htmlBody)

	// Остальной код остается без изменений...
	tlsConfig := &tls.Config{
		ServerName:         s.host,
		InsecureSkipVerify: false,
	}

	conn, err := tls.Dial("tcp", s.host+":"+s.port, tlsConfig)
	if err != nil {
		return fmt.Errorf("ошибка подключения к серверу: %w", err)
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("ошибка создания SMTP клиента: %w", err)
	}
	defer func(client *smtp.Client) {
		err := client.Close()
		if err != nil {
			logger.Error("ошибка закрытия SMTP клиента в SendConfirmMail: %v", err)
		}
	}(client)

	auth := smtp.PlainAuth("", s.mail, s.pass, s.host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("ошибка аутентификации: %w", err)
	}

	if err := client.Mail(s.mail); err != nil {
		return fmt.Errorf("ошибка указания отправителя: %w", err)
	}

	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("ошибка указания получателя: %w", err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("ошибка подготовки данных: %w", err)
	}

	_, err = w.Write(message)
	if err != nil {
		return fmt.Errorf("ошибка записи сообщения: %w", err)
	}

	return w.Close()
}

func (s *SMTP) SendResetPasswordMail(lang, recipient, resetToken string) error {
	passRecovery := s.end.TranslateMessageWithLang(lang, "password.recovery")
	forResetPass := s.end.TranslateMessageWithLang(lang, "for.reset.password")
	resetPass := s.end.TranslateMessageWithLang(lang, "reset.password")
	ifYouHavent := s.end.TranslateMessageWithLang(lang, "if.you.havenet.requested.password.reset")
	sincerely := s.end.TranslateMessageWithLang(lang, "sincerely.marusia.team")

	// Получатель и текст письма
	to := []string{recipient}

	// Формирование HTML-письма со ссылкой для сброса пароля
	resetLink := fmt.Sprintf("https://%s/reset?key=%s", mode.GetRealHost(), resetToken)

	// Обязательные заголовки для предотвращения попадания в спам
	domain := extractDomain(s.mail)
	from := fmt.Sprintf("From: MarusiaAI <%s>\r\n", s.mail)
	toHeader := fmt.Sprintf("To: %s\r\n", recipient)
	replyTo := fmt.Sprintf("Reply-To: %s\r\n", s.mail)
	date := fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	messageID := fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.New().String(), domain)
	subject := fmt.Sprintf("Subject: %s\r\n", encodeSubject(passRecovery))
	xMailer := "X-Mailer: MarusiaAI Mailer v1.0\r\n"
	mime := "MIME-Version: 1.0\r\n"
	contentType := "Content-Type: text/html; charset=utf-8\r\n"

	//	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
	//<html lang="ru">
	//<head><meta charset="UTF-8"><title>Восстановление пароля</title></head>
	//<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
	//    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
	//        <h2 style="color: #4a90e2;">Восстановление пароля</h2>
	//        <p>Для сброса вашего пароля, пожалуйста, перейдите по следующей ссылке:</p>
	//        <p style="text-align: center; margin: 30px 0;">
	//            <a href="%s" style="display: inline-block; padding: 12px 24px; background-color: #4a90e2; color: #ffffff; text-decoration: none; border-radius: 4px;">Сбросить пароль</a>
	//        </p>
	//        <p style="color: #666; font-size: 14px;">Если вы не запрашивали сброс пароля, просто проигнорируйте это письмо.</p>
	//        <p style="color: #666; font-size: 14px;">Ссылка действительна в течение ограниченного времени.</p>
	//        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
	//        <p style="color: #999; font-size: 12px;">С уважением,<br>Команда MarusiaAI</p>
	//    </div>
	//</body>
	//</html>`, resetLink)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="UTF-8"><title>%s</title></head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #4a90e2;">%s</h2>
        <p>%s</p>
        <p style="text-align: center; margin: 30px 0;">
            <a href="%s" style="display: inline-block; padding: 12px 24px; background-color: #4a90e2; color: #ffffff; text-decoration: none; border-radius: 4px;">%s</a>
        </p>
        %s
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #999; font-size: 12px;">%s</p>
    </div>
</body>
</html>`, passRecovery, passRecovery, forResetPass, resetLink, resetPass, ifYouHavent, sincerely)

	message := []byte(from + toHeader + replyTo + date + messageID + subject + xMailer + mime + contentType + "\r\n" + htmlBody)

	// Настройка TLS конфигурации
	tlsConfig := &tls.Config{
		ServerName:         s.host,
		InsecureSkipVerify: false,
	}

	// Создание защищенного соединения
	conn, err := tls.Dial("tcp", s.host+":"+s.port, tlsConfig)
	if err != nil {
		return fmt.Errorf("ошибка подключения к серверу: %w", err)
	}

	// Создание клиента SMTP поверх TLS соединения
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("ошибка создания SMTP клиента: %w", err)
	}
	defer func(client *smtp.Client) {
		err := client.Close()
		if err != nil {
			logger.Error("ошибка закрытия SMTP клиента: %v\n", err)
		}
	}(client)

	// Аутентификация
	auth := smtp.PlainAuth("", s.mail, s.pass, s.host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("ошибка аутентификации: %w", err)
	}

	// Установка отправителя и получателя
	if err := client.Mail(s.mail); err != nil {
		return fmt.Errorf("ошибка указания отправителя: %w", err)
	}

	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("ошибка указания получателя: %w", err)
		}
	}

	// Отправка сообщения
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("ошибка подготовки данных: %w", err)
	}

	_, err = w.Write(message)
	if err != nil {
		return fmt.Errorf("ошибка записи сообщения: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("ошибка завершения отправки: %w", err)
	}

	return nil
}

// SendCarpinteroVerification отправляет уведомление в AiR_TgBot - carpintero
func (s *SMTP) SendCarpinteroVerification(userId uint64, message string) error {
	const url = "http://tgbot:8080/tgbot/verification"

	// Создаем данные для отправки
	payload := map[string]any{
		"id":  userId,
		"msg": message,
	}

	// Преобразуем данные в JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("ошибка при преобразовании данных в JSON: %w", err)
	}

	// Создаем HTTP-запрос
	respCtx, cancel := context.WithTimeout(context.Background(), config.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(respCtx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("ошибка при создании HTTP-запроса: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")

	// Кастомный клиент с отключённой проверкой сертификата
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	// Отправляем запрос
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка при отправке HTTP-запроса: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logger.Error("ошибка закрытия ответа: %v\n", err)
		}
	}(resp.Body)

	// Проверяем статус ответа
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("неожиданный статус ответа: %d, тело: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

func (s *SMTP) SendNotificationMail(userId uint32, recipient, msg string) error {
	// Получатель и текст письма
	to := []string{recipient}

	notification := s.end.TranslateMessageWithUserID(userId, "notification")
	notificationFromMarusia := s.end.TranslateMessageWithUserID(userId, "notification.from.marus2ia")
	sincerelyMarusiaTeam := s.end.TranslateMessageWithUserID(userId, "sincerely.marusia.team")

	// Обязательные заголовки для предотвращения попадания в спам
	domain := extractDomain(s.mail)
	from := fmt.Sprintf("From: MarusiaAI <%s>\r\n", s.mail)
	toHeader := fmt.Sprintf("To: %s\r\n", recipient)
	replyTo := fmt.Sprintf("Reply-To: %s\r\n", s.mail)
	date := fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	messageID := fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.New().String(), domain)
	subject := fmt.Sprintf("Subject: %s\r\n", encodeSubject(notification))
	xMailer := "X-Mailer: MarusiaAI Mailer v1.0\r\n"
	mime := "MIME-Version: 1.0\r\n"
	contentType := "Content-Type: text/html; charset=utf-8\r\n"

	//	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
	//<html lang="ru">
	//<head><meta charset="UTF-8"><title>Уведомление</title></head>
	//<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
	//    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
	//        <h2 style="color: #4a90e2;">Уведомление от MarusiaAI</h2>
	//        <p>%s</p>
	//        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
	//        <p style="color: #999; font-size: 12px;">С уважением,<br>Команда MarusiaAI</p>
	//    </div>
	//</body>
	//</html>`, msg)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="UTF-8"><title>%s</title></head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #4a90e2;">%s</h2>
        <p>%s</p>
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #999; font-size: 12px;">%s</p>
    </div>
</body>
</html>`, notification, notificationFromMarusia, msg, sincerelyMarusiaTeam)

	message := []byte(from + toHeader + replyTo + date + messageID + subject + xMailer + mime + contentType + "\r\n" + htmlBody)

	// Настройка TLS конфигурации
	tlsConfig := &tls.Config{
		ServerName:         s.host,
		InsecureSkipVerify: false,
	}

	// Создание защищенного соединения
	conn, err := tls.Dial("tcp", s.host+":"+s.port, tlsConfig)
	if err != nil {
		return fmt.Errorf("ошибка подключения к серверу: %w", err)
	}

	// Создание клиента SMTP поверх TLS соединения
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("ошибка создания SMTP клиента: %w", err)
	}
	defer func(client *smtp.Client) {
		err := client.Close()
		if err != nil {
			logger.Error("ошибка закрытия SMTP клиента: %v\n", err)
		}
	}(client)

	// Аутентификация
	auth := smtp.PlainAuth("", s.mail, s.pass, s.host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("ошибка аутентификации: %w", err)
	}

	// Установка отправителя и получателя
	if err := client.Mail(s.mail); err != nil {
		return fmt.Errorf("ошибка указания отправителя: %w", err)
	}

	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("ошибка указания получателя: %w", err)
		}
	}

	// Отправка сообщения
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("ошибка подготовки данных: %w", err)
	}

	_, err = w.Write(message)
	if err != nil {
		return fmt.Errorf("ошибка записи сообщения: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("ошибка завершения отправки: %w", err)
	}

	return nil
}
