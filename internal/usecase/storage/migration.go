package storageusecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ikermy/air_logger/v2/pkg/logger"

	infra "air_orchestrator/internal/infrastructure/storage"
)

type MigrationState string

const (
	// MigrationPending Состояния durable migration job в MySQL.
	MigrationPending   MigrationState = "pending"
	MigrationRunning   MigrationState = "running"
	MigrationFailed    MigrationState = "failed"
	MigrationCompleted MigrationState = "completed"
	MigrationCancelled MigrationState = "cancelled"
)

type MigrationRecord struct {
	ID                      uint64
	UserID                  uint32
	State                   MigrationState
	Copied, Verified, Total int
	Deleted                 int
	LastError               string
	VerifiedKeys            []string
	UpdatedAt               time.Time
}

type MigrationStore interface {
	CreateMigration(context.Context, uint32, infra.BackendType, infra.BackendType) (MigrationRecord, error)
	UpdateMigration(context.Context, MigrationRecord) error
	GetMigration(context.Context, uint64) (MigrationRecord, error)
	ListPendingMigrations(context.Context, int) ([]MigrationRecord, error)
}
type MigrationPairResolver func(context.Context, uint32) (infra.Storage, infra.Storage, error)

// MigrationCanceller отменяет job, который ещё не выполняется.

type MigrationCanceller interface {
	CancelMigration(context.Context, uint64) error
}
type MigrationLocker interface {
	TryLock(context.Context, string, string, time.Duration) (bool, error)
	Unlock(context.Context, string, string) error
}
type MigrationLockRenewer interface {
	RenewLock(context.Context, string, string, time.Duration) error
}

type MigrationService struct {
	store        MigrationStore
	resolver     infra.StorageResolver
	mu           sync.Mutex
	running      map[uint64]bool
	cutover      func(context.Context, uint32) error
	locker       MigrationLocker
	pairResolver MigrationPairResolver
}

const maxMigrationObjectSize = 512 << 20

func NewMigrationService(store MigrationStore, resolver infra.StorageResolver) *MigrationService {
	return &MigrationService{store: store, resolver: resolver, running: make(map[uint64]bool)}
}

func (s *MigrationService) SetCutover(fn func(context.Context, uint32) error) {
	if s != nil {
		s.cutover = fn
	}
}
func (s *MigrationService) SetLocker(locker MigrationLocker) {
	if s != nil {
		s.locker = locker
	}
}
func (s *MigrationService) SetPairResolver(resolver MigrationPairResolver) {
	if s != nil {
		s.pairResolver = resolver
	}
}

func (s *MigrationService) ProcessPending(ctx context.Context) error {
	if s == nil || s.store == nil || s.pairResolver == nil {
		return fmt.Errorf("migration processor is not configured")
	}
	jobs, err := s.store.ListPendingMigrations(ctx, 10)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		source, target, resolveErr := s.pairResolver(ctx, job.UserID)
		if resolveErr != nil {
			if firstErr == nil {
				firstErr = resolveErr
			}
			continue
		}
		if runErr := s.Run(ctx, job.ID, job.UserID, source, target); runErr != nil && firstErr == nil {
			firstErr = runErr
		}
	}
	return firstErr
}

// Start создаёт durable job и запускает миграцию. Backend’ы передаются явно,
// чтобы переключение internal/external выполнялось после валидации конфигурации.
func (s *MigrationService) Start(ctx context.Context, userID uint32, source, target infra.Storage) (MigrationRecord, error) {
	return s.StartWithTypes(ctx, userID, source, target, infra.BackendInternal, infra.BackendExternal)
}

func (s *MigrationService) StartWithTypes(ctx context.Context, userID uint32, source, target infra.Storage, sourceType, targetType infra.BackendType) (MigrationRecord, error) {
	if s == nil || s.store == nil {
		return MigrationRecord{}, fmt.Errorf("migration store is not configured")
	}
	record, err := s.store.CreateMigration(ctx, userID, sourceType, targetType)
	if err != nil {
		return MigrationRecord{}, err
	}
	if err := s.Run(ctx, record.ID, userID, source, target); err != nil {
		return record, err
	}
	return s.store.GetMigration(ctx, record.ID)
}

// CopyAndVerify выполняет только data-plane фазу. Source не удаляется и active
// backend не переключается; это безопасная стадия перед DB commit.
func (s *MigrationService) CopyAndVerify(ctx context.Context, id uint64, userID uint32, source, target infra.Storage) (MigrationRecord, error) {
	if s == nil || source == nil || target == nil || userID == 0 {
		return MigrationRecord{}, fmt.Errorf("invalid migration")
	}
	objects, err := source.ListObjects(ctx, fmt.Sprintf("users/%d/", userID), infra.ListOptions{})
	if err != nil {
		return MigrationRecord{}, err
	}
	sort.Slice(objects.Objects, func(i, j int) bool { return objects.Objects[i].Key < objects.Objects[j].Key })
	record := MigrationRecord{ID: id, UserID: userID, State: MigrationRunning, Total: len(objects.Objects), UpdatedAt: time.Now()}
	for _, object := range objects.Objects {
		reader, info, err := source.GetObject(ctx, object.Key)
		if err != nil {
			record.State = MigrationFailed
			record.LastError = err.Error()
			return record, err
		}
		payload, err := io.ReadAll(io.LimitReader(reader, maxMigrationObjectSize+1))
		_ = reader.Close()
		if err != nil {
			record.State = MigrationFailed
			record.LastError = err.Error()
			return record, err
		}
		if int64(len(payload)) > maxMigrationObjectSize {
			err = fmt.Errorf("object exceeds migration size limit")
		} else {
			_, err = target.PutObject(ctx, object.Key, bytes.NewReader(payload), info.Size, infra.PutOptions{})
		}
		if err == nil {
			var check infra.ObjectInfo
			check, err = target.StatObject(ctx, object.Key)
			if err == nil && (check.Size != info.Size || (info.ETag != "" && check.ETag != "" && info.ETag != check.ETag)) {
				err = fmt.Errorf("target verification failed for %s", object.Key)
			}
		}
		if err != nil {
			record.State = MigrationFailed
			record.LastError = err.Error()
			return record, err
		}
		record.Copied++
		record.Verified++
		record.VerifiedKeys = append(record.VerifiedKeys, object.Key)
	}
	record.State = MigrationCompleted
	record.UpdatedAt = time.Now()
	return record, nil
}

// Run выполняет миграцию в безопасном порядке: copy, verify, cutover и только
// затем cleanup source. Прогресс и manifest ключей сохраняются после каждого
// объекта, поэтому job можно продолжить после сбоя или перезапуска процесса.
func (s *MigrationService) Run(ctx context.Context, id uint64, userID uint32, source, target infra.Storage) error {
	if s == nil || source == nil || target == nil || userID == 0 {
		return fmt.Errorf("invalid migration")
	}

	s.mu.Lock()

	if s.running[id] {
		s.mu.Unlock()
		return fmt.Errorf("migration already running")
	}

	s.running[id] = true

	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	lockToken := uuid.NewString()
	lockKey := fmt.Sprintf("storage:migration:user:%d", userID)
	if s.locker != nil {
		acquired, lockErr := s.locker.TryLock(ctx, lockKey, lockToken, 30*time.Minute)
		if lockErr != nil {
			return s.fail(ctx, id, lockErr)
		}
		if !acquired {
			return s.fail(ctx, id, fmt.Errorf("another migration is running for user"))
		}
		defer func() {
			if unlockErr := s.locker.Unlock(context.Background(), lockKey, lockToken); unlockErr != nil {
				logger.Warn("storage migration: failed to release lock %q: %v", lockKey, unlockErr, userID)
			}
		}()

		if renewer, ok := s.locker.(MigrationLockRenewer); ok {
			renewCtx, stopRenew := context.WithCancel(ctx)
			defer stopRenew()
			go renewMigrationLock(renewCtx, renewer, lockKey, lockToken)
		}
	}
	if s.store != nil {
		if current, err := s.store.GetMigration(ctx, id); err == nil && current.State == MigrationCancelled {
			return fmt.Errorf("migration cancelled")
		}
	}

	objects, err := source.ListObjects(ctx, fmt.Sprintf("users/%d/", userID), infra.ListOptions{})

	if err != nil {
		return s.fail(ctx, id, err)
	}
	sort.Slice(objects.Objects, func(i, j int) bool { return objects.Objects[i].Key < objects.Objects[j].Key })
	record := MigrationRecord{ID: id, UserID: userID, State: MigrationRunning, Total: len(objects.Objects), UpdatedAt: time.Now()}
	resumeFrom := 0
	verifiedKeys := make(map[string]struct{})
	if s.store != nil {
		if previous, loadErr := s.store.GetMigration(ctx, id); loadErr == nil {
			resumeFrom = previous.Verified
			record.VerifiedKeys = append(record.VerifiedKeys, previous.VerifiedKeys...)
			for _, key := range previous.VerifiedKeys {
				verifiedKeys[key] = struct{}{}
			}
			if resumeFrom > len(objects.Objects) {
				resumeFrom = 0
			}
		}
	}

	if s.store != nil {
		_ = s.store.UpdateMigration(ctx, record)
	}
	for index, object := range objects.Objects {
		if _, verified := verifiedKeys[object.Key]; verified || (len(verifiedKeys) == 0 && index < resumeFrom) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return s.fail(ctx, id, err)
		}
		reader, info, err := source.GetObject(ctx, object.Key)
		if err != nil {
			return s.fail(ctx, id, err)
		}
		limited := io.LimitReader(reader, maxMigrationObjectSize+1)
		payload, readErr := io.ReadAll(limited)
		_ = reader.Close()
		if readErr != nil {
			return s.fail(ctx, id, readErr)
		}
		if int64(len(payload)) > maxMigrationObjectSize || info.Size > maxMigrationObjectSize {
			return s.fail(ctx, id, fmt.Errorf("object %s exceeds migration size limit", object.Key))
		}
		copyErr := retryMigration(ctx, func() error {
			_, err := target.PutObject(ctx, object.Key, bytes.NewReader(payload), info.Size, infra.PutOptions{})
			return err
		})
		if copyErr != nil {
			return s.fail(ctx, id, copyErr)
		}
		var check infra.ObjectInfo
		check, err = target.StatObject(ctx, object.Key)
		if err != nil {
			err = retryMigration(ctx, func() error { var statErr error; check, statErr = target.StatObject(ctx, object.Key); return statErr })
		}
		if err != nil || check.Size != info.Size || (info.ETag != "" && check.ETag != "" && info.ETag != check.ETag) {
			if err == nil {
				err = fmt.Errorf("target verification failed for %s", object.Key)
			}
			return s.fail(ctx, id, err)
		}

		record.Copied = index + 1
		record.Verified = index + 1
		record.VerifiedKeys = append(record.VerifiedKeys, object.Key)
		verifiedKeys[object.Key] = struct{}{}
		record.UpdatedAt = time.Now()

		if s.store != nil {
			if err := s.store.UpdateMigration(ctx, record); err != nil {
				return s.fail(ctx, id, err)
			}
		}
	}
	if s.cutover != nil {
		// Cutover выполняется до удаления source. При ошибке старый backend
		// остаётся доступным для rollback и повторного запуска job.
		if err := s.cutover(ctx, userID); err != nil {
			return s.fail(ctx, id, fmt.Errorf("backend cutover failed: %w", err))
		}
	}

	for index, object := range objects.Objects {
		if err := ctx.Err(); err != nil {
			return s.fail(ctx, id, err)
		}
		if err := retryMigration(ctx, func() error { return source.DeleteObject(ctx, object.Key) }); err != nil {
			return s.fail(ctx, id, err)
		}
		record.Deleted = index + 1
		record.UpdatedAt = time.Now()
		if s.store != nil {
			if err := s.store.UpdateMigration(ctx, record); err != nil {
				return s.fail(ctx, id, err)
			}
		}
	}

	record.State = MigrationCompleted
	record.UpdatedAt = time.Now()
	if s.store != nil {
		return s.store.UpdateMigration(ctx, record)
	}
	return nil
}

func retryMigration(ctx context.Context, operation func() error) error {
	// Временные сетевые ошибки повторяем с небольшим линейным backoff.
	// Отмена context немедленно прекращает ожидание и retry.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = operation(); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func renewMigrationLock(ctx context.Context, locker MigrationLockRenewer, key, token string) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if locker.RenewLock(ctx, key, token, 30*time.Minute) != nil {
				return
			}
		}
	}
}

func (s *MigrationService) Cancel(ctx context.Context, id uint64) error {
	// Активный job не переводим в cancelled принудительно: его context должен
	// быть отменён worker’ом, иначе можно получить частично удалённый source.
	if s == nil || s.store == nil {
		return fmt.Errorf("migration store is not configured")
	}
	s.mu.Lock()
	active := s.running[id]
	s.mu.Unlock()
	if active {
		return fmt.Errorf("cannot cancel active migration without cancellation context")
	}
	canceller, ok := s.store.(MigrationCanceller)
	if !ok {
		return fmt.Errorf("migration cancellation is not supported")
	}
	return canceller.CancelMigration(ctx, id)
}

func (s *MigrationService) fail(ctx context.Context, id uint64, err error) error {
	// Ошибка фиксируется в durable store, чтобы оператор мог увидеть причину
	// и повторить migration после устранения проблемы.
	if s.store != nil {
		_ = s.store.UpdateMigration(ctx, MigrationRecord{ID: id, State: MigrationFailed, LastError: err.Error(), UpdatedAt: time.Now()})
	}
	return err
}
