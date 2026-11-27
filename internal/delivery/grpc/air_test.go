package grpc

import (
	airv1 "github.com/ikermy/air-common/v2/gen/go/air/v1"
)

// Компиляционная проверка, что airServer реализует сервис (без запуска).
var _ airv1.AirDataServer = (*airServer)(nil)
