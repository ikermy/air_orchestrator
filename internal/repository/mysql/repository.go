package mysql

import (
	"context"

	_ "github.com/go-sql-driver/mysql"
	"github.com/ikermy/air-common/v2/pkg/com"
	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/idgen"
)

// Implementation — реализация repository.Repository поверх MySQL.
// Жизненным циклом подключения (закрытие, каналы завершения) управляет
// обёртка internal/db.DB.
type Implementation struct {
	*comdb.DB
	IDGen                 *idgen.Generator
	IDGenEnabled          bool
	ChannelsV2ReadEnabled bool
}

func (i *Implementation) CheckUserSubscription(provider com.SubscriptionProvider, userID uint64) error {
	return com.CheckUserSubscription(provider, userID)
}

func New(parent context.Context) (*Implementation, error) {
	base, err := comdb.New(parent)
	if err != nil {
		return nil, err
	}
	gen, err := idgen.NewFromEnv()
	if err != nil {
		return nil, err
	}
	base.SetIDGen(gen.Next)
	return &Implementation{
		DB:    base,
		IDGen: gen,
		// Новые пути включены по умолчанию. Легаси-код оставлен как аварийный
		// откат через предыдущий релиз, а не через env-флаг.
		IDGenEnabled:          true,
		ChannelsV2ReadEnabled: true,
	}, nil
}
