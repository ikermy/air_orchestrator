package cron

import (
	"context"
	"errors"
	"time"

	repository "air_orchestrator/internal/domain/repository"

	"github.com/ikermy/air_common/pkg/com"
	"github.com/ikermy/air_common/pkg/endpoint"
	"github.com/ikermy/air_logger/v2/pkg/logger"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/sheets/v4"
)

// Google OAuth живет 1 час поэтому обновляем чаще
const googleOAuthInterval = 45 * time.Minute

// cronStore объединяет интерфейсы, необходимые планировщику.
type cronStore interface {
	repository.GoogleRepository
	repository.AppConfig
	repository.UserRepository
	repository.ChannelRepository
}

// Scheduler запускает и управляет периодическими задачами.
// Поддерживает graceful shutdown через отмену контекста.
type Scheduler struct {
	ctx              context.Context
	cancel           context.CancelFunc
	store            cronStore
	end              *endpoint.Endpoint
	migrationWorker  func(context.Context)
	reservationSweep func(context.Context)
}

func New(parent context.Context, db cronStore, e *endpoint.Endpoint) *Scheduler {
	ctx, cancel := context.WithCancel(parent)
	return &Scheduler{
		ctx:    ctx,
		cancel: cancel,
		store:  db,
		end:    e,
	}
}

// SetMigrationWorker подключает фоновую обработку durable storage migrations.
func (s *Scheduler) SetMigrationWorker(worker func(context.Context)) { s.migrationWorker = worker }

// SetReservationSweep подключает периодическую очистку просроченных reservation.
func (s *Scheduler) SetReservationSweep(worker func(context.Context)) { s.reservationSweep = worker }

func (s *Scheduler) Start() {
	logger.Info("Cron: планировщик запущен")
	if s.migrationWorker != nil {
		go s.runMigrationWorker()
	}
	if s.reservationSweep != nil {
		go s.runReservationSweep()
	}

	// Google OAuth токены обновляем каждые 45 минут (они живут час)
	go func() {
		ticker := time.NewTicker(googleOAuthInterval)
		defer ticker.Stop()

		// Первый запуск сразу
		s.refreshAllGoogleTokens(s.ctx, s.getGoogleOAuthConfig(s.ctx))

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.refreshAllGoogleTokens(s.ctx, s.getGoogleOAuthConfig(s.ctx))
			}
		}
	}()

	// Проверка подписок - ежедневно в 00:01
	go func() {
		// Первый запуск сразу
		s.checkUserSubscription(s.ctx)

		for {
			now := time.Now()
			// Вычисляем время следующего запуска (сегодня в 00:01)
			nextRun := time.Date(now.Year(), now.Month(), now.Day(), 0, 1, 0, 0, now.Location())

			// Если 00:01 сегодня уже прошло, планируем на завтра
			if now.After(nextRun) {
				nextRun = nextRun.Add(24 * time.Hour)
			}

			// Создаем таймер на время ожидания
			timer := time.NewTimer(time.Until(nextRun))

			logger.Debug("Cron: следующая проверка подписок запланирована на %v", nextRun)

			select {
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.checkUserSubscription(s.ctx)
			}
		}
	}()
}

func (s *Scheduler) runMigrationWorker() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.migrationWorker(s.ctx)
		}
	}
}

func (s *Scheduler) runReservationSweep() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.reservationSweep(s.ctx)
		}
	}
}

// getGoogleOAuthConfig читает параметры Google OAuth из app_config БД.
func (s *Scheduler) getGoogleOAuthConfig(ctx context.Context) *oauth2.Config {
	get := func(key string) string {
		v, _ := s.store.GetAppConfig(ctx, key)
		return v
	}
	return &oauth2.Config{
		ClientID:     get("google_oauth.client_id"),
		ClientSecret: get("google_oauth.client_secret"),
		RedirectURL:  get("google_oauth.redirect_uri"),
		Scopes: []string{
			"email",
			"profile",
			calendar.CalendarScope,
			sheets.SpreadsheetsScope,
		},
		Endpoint: google.Endpoint,
	}
}

// Shutdown останавливает планировщик и ожидает завершения всех задач.
func (s *Scheduler) Shutdown() {
	s.cancel()
	logger.Info("Cron: все задачи остановлены")
}

// refreshAllGoogleTokens проверяет подписку каждого пользователя с Google-токеном
// и при необходимости обновляет его.
func (s *Scheduler) refreshAllGoogleTokens(ctx context.Context, cfg *oauth2.Config) {
	userIDs, err := s.store.GetUsersWithGoogleToken()
	if err != nil {
		logger.Error("googleTokenRefresh: ошибка получения списка пользователей: %v", err)
		return
	}

	if len(userIDs) == 0 {
		return
	}

	logger.Debug("googleTokenRefresh: начинаю проверку %d пользователей", len(userIDs))

	for _, userID := range userIDs {
		select {
		case <-ctx.Done():
			logger.Info("Cron: googleTokenRefresh прерван во время прохода")
			return
		default:
		}

		if err := com.CheckUserSubscription(s.store, userID); err != nil {
			var subErr *com.SubscriptionError
			if errors.As(err, &subErr) {
				logger.Debug("googleTokenRefresh: пользователь %d без активной подписки (код %d), пропуск",
					userID, subErr.Code)
			} else {
				logger.Error("googleTokenRefresh: ошибка проверки подписки пользователя %d: %v",
					userID, err)
			}
			continue
		}

		// Подписка активна — обновляем токен при необходимости
		refreshCtx, cancel := context.WithTimeout(ctx, googleOAuthInterval)
		refreshErr := s.store.RefreshGoogleTokenIfNeeded(userID, cfg)
		cancel()
		_ = refreshCtx

		if refreshErr != nil {
			logger.Error("googleTokenRefresh: ошибка обновления токена пользователя: %v",
				refreshErr, userID)
		} else {
			logger.Debug("googleTokenRefresh: токен пользователя проверен/обновлён", userID)
		}

		// Небольшая пауза между пользователями — не перегружаем Google API
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	logger.Debug("googleTokenRefresh: проход завершён, обработано %d пользователей", len(userIDs))
}

func (s *Scheduler) sendSubscriptionError(error *com.SubscriptionError) {
	// Создаю сообщение об окончании подписки
	msg := com.CarpCh{
		Event:      "subscription",
		UserName:   "",
		AssistName: "",
		Target:     "",
		UserID:     error.UserID,
	}
	// Отправляю уведомление об окончании подписки
	if s.end != nil {
		err := s.end.SendNotification(msg)
		if err != nil {
			logger.Error("Ошибка отправки уведомления об окончании подписки: %v", err)
		}
	}
}

func (s *Scheduler) checkUserSubscription(ctx context.Context) {
	users, err := s.store.UsersWithoutSubscription()
	if err != nil {
		logger.Error("checkUserSubscription: ошибка получения пользователей без подписки: %v", err)
		return
	}

	for _, user := range users {
		select {
		case <-ctx.Done():
			logger.Info("Cron: checkUserSubscription прерван во время прохода")
			return
		default:
		}

		subErr := &com.SubscriptionError{
			UserID:  user,
			Message: "подписка истекла",
			Code:    com.ErrSubscriptionExpired,
		}
		go s.sendSubscriptionError(subErr)
		go func(u uint32) {
			if err := s.store.DisableAllUserChannel(u); err != nil {
				logger.Error("checkUserSubscription: ошибка отключения каналов для пользователя %d: %v", user, err)
			}
		}(user)
	}

	if err := s.store.SetUsersSubscriptionNotified(users); err != nil {
		logger.Error("checkUserSubscription: ошибка обновления статуса уведомления для пользователей: %v", err)
	}
}
