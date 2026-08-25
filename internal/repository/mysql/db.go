package db

import (
	"air_orchestrator/internal/domain/state"
	"context"
	"sync"

	_ "github.com/go-sql-driver/mysql"
	"github.com/ikermy/air-common/pkg/com"
	"github.com/ikermy/air-common/pkg/comdb"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

type DB struct {
	*comdb.DB
	migrationDone bool
	migrationMu   sync.Mutex
}

func (d *DB) CheckUserSubscription(provider com.SubscriptionProvider, userID uint32) error {
	return com.CheckUserSubscription(provider, userID)
}

func New(parent context.Context) (*DB, error) {
	db, err := comdb.New(parent)
	if err != nil {
		return nil, err
	}
	return &DB{
		DB: db,
	}, nil
}

func (d *DB) HandlerClose() {
	go func() {
		// Получаю сигнал о завершении работы от главного контекста приложения
		<-d.MainCTX().Done()
		logger.Info("DB: контекст отменен, ожидаю завершения всех операций...")

		// Ожидаем сигнал о завершении от компонентов работающих с ДБ
		<-state.UsersDB
		logger.Info("DB: все модули работающие с БД завершили работу, продолжаю остановку...")

		if err := d.Close(); err != nil {
			logger.Error("DB: ошибка при закрытии: %v", err)
		}

		// Безопасно закрываем канал Exit (защита от panic при множественном close)
		state.CloseExit()
	}()
}
