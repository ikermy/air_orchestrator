// Package notification реализует отправку административных уведомлений
// во внутренний Telegram-бот (сервис carpintero).
// Это инфраструктурный пакет — он знает о транспорте, но не о бизнес-логике.
package notification

import (
	"air_orchestrator/internal/config"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// AdminNotifier отправляет уведомления администраторам через Telegram-бот.
type AdminNotifier struct {
	client *http.Client
}

// New создаёт AdminNotifier с дефолтным HTTP-клиентом.
func New() *AdminNotifier {
	return &AdminNotifier{client: &http.Client{}}
}

// EventKind — тип административного события.
type EventKind uint8

const (
	EventNewReg      EventKind = 1
	EventMailConfirm EventKind = 2
	EventUserDelete  EventKind = 3
)

func LoadNotifTgIDs() ([]int64, error) {
	env := os.Getenv("NOTIF_TG_IDS")
	if env == "" {
		return nil, fmt.Errorf("NOTIF_TG_IDS not set")
	}

	parts := strings.Split(env, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid tg id %q: %v", p, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Notify отправляет уведомление администратору в зависимости от типа события.
func (n *AdminNotifier) Notify(event EventKind, message string) {
	adminTgIDS, err := LoadNotifTgIDs()
	if err != nil {
		logger.Error("Ошибка получения телеграм идентификаторов для уведомлений %w", err)
		return
	}

	var msg string
	switch event {
	case EventNewReg:
		msg = fmt.Sprintf("Регистрация нового пользователя\n%s", message)
	case EventMailConfirm:
		msg = fmt.Sprintf("Подтверждение email адреса пользователя\n%s", message)
	case EventUserDelete:
		msg = fmt.Sprintf("Удаление всех данных пользователя\n%s", message)
	default:
		logger.Error("AdminNotifier: неизвестный тип события: %d", event)
		return
	}

	for _, adminTgID := range adminTgIDS {
		if err := n.Send(adminTgID, msg); err != nil {
			logger.Error("AdminNotifier: ошибка отправки уведомления: %v", err)
		}
	}
}

// Send отправляет произвольное сообщение указанному Telegram-пользователю.
func (n *AdminNotifier) Send(tID int64, message string) error {
	const url = "http://tgbot:8080/tgbot/adnot"

	payload := map[string]any{
		"tid": tID,
		"msg": message,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notification.Send: marshal: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("notification.Send: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notification.Send: do request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Error("notification.Send: close body: %v", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notification.Send: unexpected status %d: %s", resp.StatusCode, body)
	}

	return nil
}
