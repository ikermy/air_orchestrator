package db

import (
	"context"
	"sync"

	repoMysql "air_orchestrator/internal/repository/mysql"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// DB — обёртка жизненного цикла подключения к MySQL поверх repoMysql.DB.
// Все методы репозитория и comdb.Exterior продвигаются через встроенный
// *repoMysql.DB, поэтому вызывающий код работает с *db.DB как с репозиторием.
type DB struct {
	*repoMysql.Implementation

	done   sync.Once     // На всякий случай однократное закрытие канала
	DoneCh chan struct{} // Канал уведомления о завершении операций пользователями ДБ
	Exit   chan struct{} // Канал завершения работы приложения
}

// New создаёт подключение к БД и оборачивает репозиторий жизненным циклом.
func New(parent context.Context) (*DB, error) {
	inner, err := repoMysql.New(parent)
	if err != nil {
		return nil, err
	}

	return &DB{
		Implementation: inner,
		DoneCh:         make(chan struct{}),
		Exit:           make(chan struct{}),
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

// IsAppConfigRekeyMode сообщает, запущено ли приложение в режиме перекодирования app_config.
func IsAppConfigRekeyMode() bool {
	return repoMysql.IsAppConfigRekeyMode()
}

// ValidateAppConfigRekeyConfig проверяет окружение для режима перекодирования app_config.
func ValidateAppConfigRekeyConfig() error {
	return repoMysql.ValidateAppConfigRekeyConfig()
}
