package app

import (
	web "air_orchestrator/internal/delivery/http"
	"air_orchestrator/internal/infrastructure/redis"
	exam "air_orchestrator/internal/infrastructure/security"
	"air_orchestrator/internal/infrastructure/storage"
	db "air_orchestrator/internal/repository/mysql"
	storageusecase "air_orchestrator/internal/usecase/storage"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// newStorageServices reads MinIO env vars, builds the storage backend and
// session service, wires migration cutover/pair/locker, creates the reservation
// service, and returns a composite web.StorageServices bundle.
func newStorageServices(d *db.DB, x *exam.Exam, redisCli *redis.Client) (*web.StorageServices, error) {
	endpoint := strings.TrimSpace(os.Getenv("REAL_URL"))
	if endpoint == "" {
		logger.Warn("REAL_URL не задан — storage недоступен")
		return &web.StorageServices{}, nil
	}

	minioBucket := strings.TrimSpace(os.Getenv("MINIO_BUCKET"))
	if minioBucket == "" {
		minioBucket = "user-files"
	}

	minioRegion := strings.TrimSpace(os.Getenv("MINIO_REGION"))
	if minioRegion == "" {
		minioRegion = "us-east-1"
	}
	minioEndpoint := strings.TrimSpace(os.Getenv("MINIO_INTERNAL_ENDPOINT"))
	if minioEndpoint == "" {
		minioEndpoint = "minio:9000"
	}

	minioAccessKey := strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY"))
	if minioAccessKey == "" {
		minioAccessKey = "root"
	}
	skFile := strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY_FILE"))
	if skFile == "" {
		return nil, fmt.Errorf("MINIO_SECRET_KEY_FILE обязателен")
	}
	minioSecretKey := strings.TrimSpace(string(readEnvFile(skFile)))
	if minioSecretKey == "" {
		return nil, fmt.Errorf("MINIO root credentials не найдены в файлах")
	}

	var storageFactory *storage.StorageFactory
	var sessionService *storage.SessionService
	var minioStorage *storage.MinIOStorage

	minioStorage, storageErr := storage.NewMinIOStorage(minioEndpoint, minioAccessKey, minioSecretKey, minioBucket)
	if storageErr != nil {
		logger.Fatal("Ошибка инициализации MinIO storage: %v", storageErr)
	}
	// Presigned URLs use the public endpoint, while an optional proxy address
	// allows containers to reach that endpoint when its public hostname is
	// localhost (development Docker setup).
	publicInsecure := strings.EqualFold(strings.TrimSpace(os.Getenv("MINIO_PUBLIC_INSECURE")), "true")
	publicProxy := strings.TrimSpace(os.Getenv("MINIO_PUBLIC_PROXY"))
	if publicErr := minioStorage.SetPublicEndpointViaProxy(endpoint, publicInsecure, publicProxy); publicErr != nil {
		return nil, fmt.Errorf("invalid MinIO public endpoint: %w", publicErr)
	}
	storageFactory = storage.NewConfiguredStorageFactory(minioStorage, d, x.GetMasterKey)
	sessionService = storage.NewSessionService("http://"+minioEndpoint, "https://"+endpoint, minioAccessKey, minioSecretKey, minioBucket, minioRegion)

	migrationService := storageusecase.NewMigrationService(d, storageFactory)

	if migrationService != nil {
		sf := storageFactory
		migrationService.SetPairResolver(func(ctx context.Context, userID uint32) (storage.Storage, storage.Storage, error) {
			cfg, cfgErr := d.StorageConfig(ctx, userID)
			if cfgErr != nil {
				return nil, nil, fmt.Errorf("failed to load config: %w", cfgErr)
			}
			var targetType storage.BackendType
			if cfg.Type == storage.BackendInternal {
				targetType = storage.BackendExternal
			} else {
				targetType = storage.BackendInternal
			}
			source, srcErr := sf.Resolve(ctx, userID)
			if srcErr != nil {
				return nil, nil, fmt.Errorf("resolve source: %w", srcErr)
			}
			target, tgtErr := sf.ResolveByType(ctx, userID, targetType)
			if tgtErr != nil {
				return nil, nil, fmt.Errorf("resolve target: %w", tgtErr)
			}
			return source, target, nil
		})

		migrationService.SetCutover(func(ctx context.Context, userID uint32) error {
			cfg, cfgErr := d.StorageConfig(ctx, userID)
			if cfgErr != nil {
				return cfgErr
			}
			var newType storage.BackendType
			if cfg.Type == storage.BackendInternal {
				newType = storage.BackendExternal
			} else {
				newType = storage.BackendInternal
			}
			return d.SaveStorageConfig(ctx, storage.BackendConfig{
				UserID:              userID,
				Type:                newType,
				Endpoint:            cfg.Endpoint,
				Bucket:              cfg.Bucket,
				Region:              cfg.Region,
				AccessKeyCiphertext: cfg.AccessKeyCiphertext,
				SecretKeyCiphertext: cfg.SecretKeyCiphertext,
			})
		})

		if redisCli != nil {
			migrationService.SetLocker(redisCli)
		}
	}

	var reservationService *storageusecase.ReservationService
	if redisCli != nil && d != nil && minioStorage != nil {
		reservationService = storageusecase.NewReservationService(redisCli, minioStorage, d)
	}

	if redisCli != nil {
		if storageFactory != nil {
			storageFactory.SetCache(redisCli)
		}
		if sessionService != nil {
			sessionService.SetCache(redisCli)
		}
	}

	return &web.StorageServices{
		Factory:      storageFactory,
		Sessions:     sessionService,
		Reservations: reservationService,
		Migrations:   migrationService,
	}, nil
}

func readEnvFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		logger.Fatal("Не удалось прочитать файл credentials: %s: %v", path, err)
	}
	return b
}
