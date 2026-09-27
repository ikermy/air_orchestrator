package db

import (
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

	done   sync.Once     // На всякий случай однократное закрытие канала
	DoneCh chan struct{} // Канал уведомления о завершении операций пользователями ДБ
	Exit   chan struct{} // Канал завершения работы приложения
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
		DB:     db,
		DoneCh: make(chan struct{}),
		Exit:   make(chan struct{}),
	}, nil
}

func (d *DB) HandlerClose() {
	go func() {
		// Получаю сигнал о завершении работы от главного контекста приложения
		<-d.MainCTX().Done()
		logger.Info("DB: контекст отменен, ожидаю завершения всех операций...")

		// Ожидаем сигнал о завершении от компонентов работающих с ДБ
		<-d.DoneCh
		logger.Info("DB: все модули работающие с БД завершили работу, продолжаю остановку...")

		if err := d.Close(); err != nil {
			logger.Error("DB: ошибка при закрытии: %v", err)
		}

		close(d.Exit)
	}()
}

func (d *DB) CloseDoneCh() {
	d.done.Do(func() {
		close(d.DoneCh)
	})
}

func (d *DB) GetExitCh() <-chan struct{} {
	return d.Exit
}
