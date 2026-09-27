package mysql

import (
	"context"
	"sync"

	_ "github.com/go-sql-driver/mysql"
	"github.com/ikermy/air-common/pkg/com"
	"github.com/ikermy/air-common/pkg/comdb"
)

// Implementation — реализация repository.Repository поверх MySQL.
// Жизненным циклом подключения (закрытие, каналы завершения) управляет
// обёртка internal/db.DB.
type Implementation struct {
	*comdb.DB
	migrationDone bool
	migrationMu   sync.Mutex
}

func (i *Implementation) CheckUserSubscription(provider com.SubscriptionProvider, userID uint32) error {
	return com.CheckUserSubscription(provider, userID)
}

func New(parent context.Context) (*Implementation, error) {
	base, err := comdb.New(parent)
	if err != nil {
		return nil, err
	}
	return &Implementation{DB: base}, nil
}
