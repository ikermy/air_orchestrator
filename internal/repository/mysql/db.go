package db

import (
	"air_orchestrator/internal/domain/state"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/ikermy/air_common/pkg/com"
	"github.com/ikermy/air_common/pkg/comdb"
	"github.com/ikermy/air_common/pkg/crypto"
	"github.com/ikermy/air_common/pkg/mode"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_logger/v2/pkg/logger"
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

func (d *DB) CheckEmail(email, emailHMAC string) (uint32, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Ищем по plaintext Email (старые пользователи) ИЛИ по EmailHash (мигрированные/новые).
	var result sql.NullInt32
	err := d.Conn().QueryRowContext(ctx,
		"SELECT UserId FROM user_auth WHERE Email = ? OR EmailHash = ? LIMIT 1",
		email, emailHMAC).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при проверке email: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return 0, nil
		default:
			return 0, fmt.Errorf("ошибка проверки email: %w", err)
		}
	}

	// Проверяем, было ли значение NULL
	if !result.Valid {
		return 0, nil
	}

	return uint32(result.Int32), nil
}

func (d *DB) CreateUser(name, pass, encEmail, emailHMAC, lang string, demo bool) (uint32, error) {
	// Проверяю что нет пустых значений
	if name == "" || pass == "" || encEmail == "" || emailHMAC == "" {
		return 0, fmt.Errorf("получены пустые значения")
	}

	// Проверяю что lang валидный
	ok := state.ValidateLanguage(lang)
	if !ok {
		lang = "ru"
	}

	// Если пользователь выбрал демо доступ
	role := 2
	if demo {
		role = 1
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в CreateUser: %v", rbErr)
		}
	}()

	// Определяем langId на основе языка
	var langId uint8 = 1 // По умолчанию русский да, второй раз страхуюсь
	err = tx.QueryRowContext(ctx, "SELECT Id FROM languages WHERE Code = ? LIMIT 1", lang).Scan(&langId)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при получении языка: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка получения языка: %w", err)
		}
	}

	// Пока все будут с USDT (currency = 0) state.DefaultCurrency

	// Вставляем пользователя в таблицу users
	result, err := tx.ExecContext(ctx,
		"INSERT INTO users (`Name`, `RoleId`, `Date`, `currency`, `Lang`) VALUES (?, ?, CURRENT_TIMESTAMP(), ?, ?)",
		name, role, state.DefaultCurrency, langId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при создании пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания пользователя: %w", err)
		}
	}

	// Получаем ID нового пользователя
	newUserId, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("ошибка получения ID пользователя: %w", err)
	}

	// Вставляем данные авторизации в таблицу user_auth
	_, err = tx.ExecContext(ctx,
		"INSERT INTO user_auth (`UserId`, `SHA`, `Email`, `EmailHash`, `Confirmed`) VALUES (?, ?, ?, ?, 0)",
		newUserId, pass, encEmail, emailHMAC)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при создании авторизации: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания авторизации: %w", err)
		}
	}

	// Добавляем запись в subscriptions в зависимости от типа пользователя
	if role == 2 {
		// Обычная подписка
		_, err = tx.ExecContext(ctx,
			"INSERT INTO subscriptions (`UserId`, `StartDate`, `TotalCost`) VALUES (?, CURRENT_DATE(), 0)",
			newUserId)
	} else {
		// Демо-подписка (один месяц)
		_, err = tx.ExecContext(ctx,
			"INSERT INTO subscriptions (`UserId`, `StartDate`, `EndDate`, `TotalCost`) VALUES (?, CURRENT_DATE(), DATE_ADD(CURRENT_DATE(), INTERVAL 1 MONTH), 0)",
			newUserId)
	}
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при создании подписки: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция создании подписки отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания подписки: %w", err)
		}
	}

	// Создаю лимит в хранилище пользователя
	_, err = tx.ExecContext(ctx,
		"INSERT INTO user_storage_quota (`user_id`, `quota_bytes`) VALUES (?, ?)",
		newUserId, state.NewUserStorageLimit)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при задании лимита хранилища: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("при задании лимита хранилища операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания лимита хранилища: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return uint32(newUserId), nil
}

func (d *DB) CheckAuth(pass, email string) (json.RawMessage, error) {
	// Проверяем входные значения
	if pass == "" || email == "" {
		return nil, fmt.Errorf("получены пустые значения")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос для получения данных пользователя
	query := `
  SELECT JSON_OBJECT(
   'Id', u.Id,
   'Confirmed', ua.Confirmed,
   'Disabled', ua.Disabled
  )
  FROM users u
  JOIN user_auth ua ON ua.UserId = u.Id
  JOIN user_roles ur ON u.RoleId = ur.Id
  LEFT JOIN currency c ON u.currency = c.Id
  WHERE ua.SHA = ? AND ua.Email = ?
  LIMIT 1`

	var result sql.NullString
	err := d.Conn().QueryRowContext(ctx, query, pass, email).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при проверке авторизации: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil
		default:
			return nil, fmt.Errorf("ошибка проверки авторизации: %w", err)
		}
	}

	// Проверяем корректность результата
	if !result.Valid || result.String == "" {
		return nil, nil
	}

	return json.RawMessage(result.String), nil
}

func (d *DB) ConfirmMail(email, emailHMAC string) error {
	if email == "" && emailHMAC == "" {
		return fmt.Errorf("получены пустые значения")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Поиск по Email (старые пользователи) или по EmailHash (новые/мигрированные)
	result, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET Confirmed = 1 WHERE (Email = ? OR EmailHash = ?) AND Confirmed = 0",
		email, emailHMAC)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при подтверждении email: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка подтверждения email: %w", err)
		}
	}

	// Проверяем, была ли обновлена хотя бы одна строка
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка получения количества обновленных строк: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("ошибка подтверждения email")
	}

	return nil
}

func (d *DB) UpdatePassword(email, emailHMAC string, newSHA string) error {
	// Проверяю что нет пустых значений
	if (email == "" && emailHMAC == "") || newSHA == "" {
		return fmt.Errorf("получены пустые значения")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Поиск по Email (старые пользователи) или по EmailHash (новые/мигрированные)
	result, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET SHA = ? WHERE Email = ? OR EmailHash = ?",
		newSHA, email, emailHMAC)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при обновлении пароля: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка обновления пароля: %w", err)
		}
	}

	// Проверяем, была ли обновлена хотя бы одна строка
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка получения количества обновленных строк: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("ошибка обновления пароля, пользователь не найден")
	}

	return nil
}

func (d *DB) GetUserDetails(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос для получения данных пользователя и подписки
	query := `
  SELECT JSON_OBJECT(
 'Date', u.Date,
 'RoleName', ur.RoleName,
 'Name', u.Name,
 'Balance', u.balance,
 'CurrencyName', cur.Name,
 'Confirmed', ua.Confirmed,
 'Disabled', ua.Disabled,
 'StartDate', s.StartDate,
 'MonthsPaid', s.MonthsPaid,
 'TotalCost', s.TotalCost,
 'Discount', s.Discount,
 'EndDate', s.EndDate,
 'StorageLimit', us.quota_bytes,
 'StorageUsed', us.used_bytes,
 'Telegram_bot', ch.TgBot_enabled,
 'Telegram_user', ch.TgUserBot_enabled,
 'WhatsApp', ch.Whats_enabled,
 'Widget', ch.Widget_enabled,
 'Instagram', ch.Insta_enabled,
 'Avito', ch.Avito_enabled
)
FROM users u
JOIN user_auth ua ON ua.UserId = u.Id
JOIN user_roles ur ON u.RoleId = ur.Id
LEFT JOIN currency cur ON u.currency = cur.Id
LEFT JOIN subscriptions s ON s.UserId = u.Id
LEFT JOIN user_storage_quota us ON u.Id = us.user_id
LEFT JOIN channels ch ON ch.UserId = u.Id
WHERE u.Id = ?
LIMIT 1`

	var result []byte
	err := d.Conn().QueryRowContext(ctx, query, userId).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении данных пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("пользователь не найден")
		default:
			return nil, fmt.Errorf("ошибка получения данных пользователя: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от GetUserDetails")
	}

	return result, nil
}

// RemoveModelFromUser удаляет связь между пользователем и моделью в таблице user_models
// Также удаляет саму модель из user_gpt, если это была последняя связь с этой моделью
func (d *DB) RemoveModelFromUser(userId uint32, modelId uint64) error {
	// Проверяем входные значения
	if userId == 0 || modelId == 0 {
		return fmt.Errorf("получены некорректные значения: userId или modelId равны 0")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в RemoveModelFromUser: %v", rbErr, userId)
		}
	}()

	// Проверяем, существует ли связь пользователя с моделью
	var exists bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM user_models WHERE UserId = ? AND ModelId = ?)",
		userId, modelId).Scan(&exists)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при проверке связи пользователя с моделью: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена при проверке связи: %w", err)
		default:
			return fmt.Errorf("ошибка проверки связи пользователя с моделью: %w", err)
		}
	}

	if !exists {
		return fmt.Errorf("связь между пользователем %d и моделью %d не найдена", userId, modelId)
	}

	// Проверяем, была ли эта модель активной
	var wasActive bool
	err = tx.QueryRowContext(ctx,
		"SELECT IsActive FROM user_models WHERE UserId = ? AND ModelId = ?",
		userId, modelId).Scan(&wasActive)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при проверке активности модели: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена при проверке активности: %w", err)
		default:
			return fmt.Errorf("ошибка проверки активности модели: %w", err)
		}
	}

	// Удаляем связь между пользователем и моделью
	_, err = tx.ExecContext(ctx,
		"DELETE FROM user_models WHERE UserId = ? AND ModelId = ?",
		userId, modelId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении связи: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена при удалении связи: %w", err)
		default:
			return fmt.Errorf("ошибка удаления связи пользователя с моделью: %w", err)
		}
	}

	// Проверяем, есть ли у этой модели другие связи с пользователями
	var otherUsersCount int
	err = tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_models WHERE ModelId = ?",
		modelId).Scan(&otherUsersCount)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при проверке других связей модели: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена при проверке других связей: %w", err)
		default:
			return fmt.Errorf("ошибка проверки других связей модели: %w", err)
		}
	}

	// Если других связей нет, удаляем саму модель из user_gpt
	if otherUsersCount == 0 {
		_, err = tx.ExecContext(ctx, "DELETE FROM user_gpt WHERE Id = ?", modelId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при удалении модели: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при удалении модели: %w", err)
			default:
				return fmt.Errorf("ошибка удаления модели: %w", err)
			}
		}
	}

	// Если удалённая модель была активной, нужно активировать другую модель (если есть)
	if wasActive {
		// Получаем первую доступную модель пользователя
		var nextModelId sql.NullInt64
		err = tx.QueryRowContext(ctx,
			"SELECT ModelId FROM user_models WHERE UserId = ? LIMIT 1",
			userId).Scan(&nextModelId)

		// Если есть другая модель, делаем её активной
		if err == nil && nextModelId.Valid {
			_, err = tx.ExecContext(ctx,
				"UPDATE user_models SET IsActive = 1 WHERE UserId = ? AND ModelId = ?",
				userId, nextModelId.Int64)
			if err != nil {
				return fmt.Errorf("ошибка активации следующей модели: %w", err)
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			// Если других моделей нет - отключаем все каналы пользователя
			// Фиксируем транзакцию перед вызовом DisableAllUserChannel
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("ошибка фиксации транзакции: %w", err)
			}

			// Отключаем все каналы, так как у пользователя больше нет моделей
			if err := d.DisableAllUserChannel(userId); err != nil {
				return fmt.Errorf("ошибка отключения каналов пользователя: %w", err)
			}

			return nil
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) DeleteFileFromUserGPT(userId uint32, fileID string) error {
	// Проверяем входные значения
	if userId == 0 || fileID == "" {
		return fmt.Errorf("получены некорректные значения: userId или fileID пусты")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в DeleteFileFromUserGPT: %v", rbErr, userId)
		}
	}()

	// Формируем корректный file-id
	fullId := fileID
	if !strings.HasPrefix(fileID, "file-") {
		fullId = "file-" + fileID
	}

	// Блокируем строку и получаем JSON + PK активной модели
	var origJson sql.NullString
	var ugptId uint32
	err = tx.QueryRowContext(ctx, `
  SELECT ug.Ids, ug.Id
  FROM user_models um
  JOIN user_gpt ug ON um.ModelId = ug.Id
  WHERE um.UserId = ? AND um.IsActive = 1
  FOR UPDATE`, userId).Scan(&origJson, &ugptId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при получении данных user_gpt: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("активная модель не найдена")
		default:
			return fmt.Errorf("ошибка получения данных user_gpt: %w", err)
		}
	}

	if !origJson.Valid || origJson.String == "" {
		return fmt.Errorf("JSON данные пользователя отсутствуют")
	}

	// Парсим JSON для работы с массивом FileIds
	var idsData struct {
		FileIds []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"FileIds"`
		VectorId []string `json:"VectorId"`
	}

	if err := json.Unmarshal([]byte(origJson.String), &idsData); err != nil {
		return fmt.Errorf("ошибка разбора JSON: %w", err)
	}

	// Ищем и удаляем файл с нужным ID
	found := false
	newFileIds := make([]struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}, 0, len(idsData.FileIds))

	for _, file := range idsData.FileIds {
		if file.ID == fullId {
			found = true
			continue // Пропускаем этот элемент (удаляем его)
		}
		newFileIds = append(newFileIds, file)
	}

	if !found {
		return fmt.Errorf("файл с ID %s не найден", fullId)
	}

	// Формируем новый JSON
	idsData.FileIds = newFileIds
	prunedJson, err := json.Marshal(idsData)
	if err != nil {
		return fmt.Errorf("ошибка формирования JSON: %w", err)
	}

	// Обновляем JSON в базе данных
	_, err = tx.ExecContext(ctx, `UPDATE user_gpt SET Ids = ? WHERE Id = ?`, string(prunedJson), ugptId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении файла из GPT модели: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления идентификатора файла из userGPT: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) AddFileFromUserGPT(userId uint32, fileID, fileName string) error {
	// Проверяем входные значения
	if userId == 0 || fileID == "" || fileName == "" {
		return fmt.Errorf("получены некорректные значения: userId, fileID или fileName пусты")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в AddFileFromUserGPT: %v", rbErr, userId)
		}
	}()

	// Формируем корректный file-id
	fullId := fileID
	if !strings.HasPrefix(fileID, "file-") {
		fullId = "file-" + fileID
	}

	// Блокируем строку и получаем JSON + PK активной модели
	var origJson sql.NullString
	var ugptId uint32
	err = tx.QueryRowContext(ctx, `
  SELECT ug.Ids, ug.Id
  FROM user_models um
  JOIN user_gpt ug ON um.ModelId = ug.Id
  WHERE um.UserId = ? AND um.IsActive = 1
  FOR UPDATE`, userId).Scan(&origJson, &ugptId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при получении данных user_gpt: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("активная модель не найдена")
		default:
			return fmt.Errorf("ошибка получения данных user_gpt: %w", err)
		}
	}

	// Экранируем имя файла для JSON
	escapedFileName := strings.ReplaceAll(fileName, `"`, `\"`)

	// Собираем JSON-объект в текстовом виде
	objTxt := fmt.Sprintf(`{"name":"%s","id":"%s"}`, escapedFileName, fullId)

	// Обновляем JSON с новым элементом массива
	_, err = tx.ExecContext(ctx, `
  UPDATE user_gpt
  SET Ids = JSON_MERGE_PRESERVE(Ids, CONCAT('{"FileIds":[', ?, ']}'))
  WHERE Id = ?`, objTxt, ugptId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при добавлении файла в GPT модель: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка добавления идентификатора файла в userGPT: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

// SaveChannelData переопределяет db.go:SaveChannelData с поддержкой $mk$.
func (d *DB) SaveChannelData(userId uint32, channelType string, data string, enabled bool) error {
	if userId == 0 || channelType == "" {
		return fmt.Errorf("получены некорректные значения: userId или channelType пусты")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	var jsonData string
	if json.Valid([]byte(data)) {
		jsonData = data
	} else {
		var key string
		switch channelType {
		case "tgbot", "tgubot":
			key = "token"
		default:
			key = "value"
		}
		jsonData = fmt.Sprintf(`{%q: %q}`, key, data)
	}

	// Шифруем данные канала MasterKey'ом ($mk$) если он доступен
	if d.MasterKeyResolver != nil {
		if mk, ok := d.MasterKeyResolver(userId); ok {
			encrypted, err := crypto.EncryptFieldWithMasterKey(mk, jsonData)
			if err != nil {
				return fmt.Errorf("failed to encrypt channel data with MasterKey: %w", err)
			}
			jsonData = encrypted
		}
	}

	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в SaveChannelData: %v", rbErr, userId)
		}
	}()

	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channels WHERE UserId = ?)", userId).Scan(&exists); err != nil {
		return fmt.Errorf("ошибка проверки существования записи: %w", err)
	}
	if !exists {
		if _, err = tx.ExecContext(ctx, "INSERT INTO channels (UserId) VALUES (?)", userId); err != nil {
			return fmt.Errorf("ошибка создания записи в channels: %w", err)
		}
	}

	switch channelType {
	case "tgbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgBot = ?, TgBot_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "widget":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Widget = ?, Widget_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "tgubot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgUserBot = ?, TgUserBot_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "whatsbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Whats = ?, Whats_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "insta":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Insta = ?, Insta_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "avito":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Avito = ?, Avito_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	default:
		return fmt.Errorf("неизвестный тип канала: %s", channelType)
	}
	if err != nil {
		return fmt.Errorf("ошибка обновления канала %s: %w", channelType, err)
	}
	return tx.Commit()
}

// GetChannelsData переопределяет db.go:GetChannelsData с поддержкой $mk$-расшифровки.
func (d *DB) GetChannelsData(userId uint32) (json.RawMessage, error) {
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var exists bool
	if err := d.Conn().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channels WHERE UserId = ?)", userId).Scan(&exists); err != nil {
		return nil, fmt.Errorf("ошибка проверки существования каналов: %w", err)
	}

	type entry struct {
		Data    json.RawMessage `json:"data"`
		Enabled bool            `json:"enabled"`
	}
	empty := json.RawMessage(`{}`)

	if !exists {
		res, _ := json.Marshal(map[string]entry{
			"tgbot":     {Data: empty, Enabled: false},
			"widget":    {Data: empty, Enabled: false},
			"tguserbot": {Data: empty, Enabled: false},
			"whatsbot":  {Data: empty, Enabled: false},
			"avito":     {Data: empty, Enabled: false},
		})
		return res, nil
	}

	var (
		tgBot    sql.NullString
		tgBotEn  int
		widget   sql.NullString
		widgetEn int
		tgUBot   sql.NullString
		tgUBotEn int
		whats    sql.NullString
		whatsEn  int
		avitoStr sql.NullString
		avitoEn  sql.NullInt32
	)

	err := d.Conn().QueryRowContext(ctx, `
		SELECT TgBot, TgBot_enabled, Widget, Widget_enabled,
		       TgUserBot, TgUserBot_enabled, Whats, Whats_enabled,
		       Avito, Avito_enabled
		FROM channels WHERE UserId = ?`, userId).
		Scan(&tgBot, &tgBotEn, &widget, &widgetEn,
			&tgUBot, &tgUBotEn, &whats, &whatsEn,
			&avitoStr, &avitoEn)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ошибка получения данных каналов: %w", err)
	}

	return json.Marshal(map[string]entry{
		"tgbot":     {Data: d.decryptChannelField(userId, tgBot), Enabled: tgBotEn == 1},
		"widget":    {Data: d.decryptChannelField(userId, widget), Enabled: widgetEn == 1},
		"tguserbot": {Data: d.decryptChannelField(userId, tgUBot), Enabled: tgUBotEn == 1},
		"whatsbot":  {Data: d.decryptChannelField(userId, whats), Enabled: whatsEn == 1},
		"avito":     {Data: d.decryptChannelField(userId, avitoStr), Enabled: avitoEn.Valid && avitoEn.Int32 == 1},
	})
}

func (d *DB) DeleteChannelData(userId uint32, channelType string) error {
	// Проверяем входные значения
	if userId == 0 || channelType == "" {
		return fmt.Errorf("получены некорректные значения: userId или channelType пусты")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в DeleteChannelData: %v", rbErr, userId)
		}
	}()

	// Обновляем данные в зависимости от типа канала
	switch channelType {
	case "tgbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgBot = null, TgBot_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала TgBot: %w", err)
		}
	case "widget":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Widget = null, Widget_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Widget: %w", err)
		}
	case "tgubot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgUserBot = null, TgUserBot_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала TgUserBot: %w", err)
		}
	case "whatsbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Whats = null, Whats_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Whats: %w", err)
		}
	case "insta":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Insta = null, Insta_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Insta: %w", err)
		}
	case "avito":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Avito = null, Avito_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Avito: %w", err)
		}
	default:
		return fmt.Errorf("неизвестный тип канала: %s", channelType)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) GetUserEmail(userId uint32) (string, error) {
	// Проверяем входное значение
	if userId == 0 {
		return "", fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var data sql.NullString
	err := d.Conn().QueryRowContext(ctx, "SELECT Email FROM user_auth WHERE UserId = ?", userId).Scan(&data)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", fmt.Errorf("тайм-аут (%d с) при получении email пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return "", fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return "", nil // Email не найден, но это не ошибка
		default:
			return "", fmt.Errorf("ошибка получения email пользователя: %w", err)
		}
	}

	if !data.Valid {
		return "", nil // Возвращаем пустую строку если данные NULL
	}

	return data.String, nil
}

func (d *DB) UpdateNotification(userId uint32, tip string, status bool, telegaId uint64) error {
	// Проверяем входные значения
	if userId == 0 || tip == "" {
		return fmt.Errorf("получены некорректные значения: userId или tip пусты")
	}

	// Приводим название типа к нижнему регистру для унификации
	tip = strings.ToLower(tip)

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в UpdateNotification: %v", rbErr, userId)
		}
	}()

	// Проверяем существование записи для данного UserId
	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM notifications WHERE UserId = ?)", userId).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ошибка проверки существования записи: %w", err)
	}

	enabledInt := 0
	if status {
		enabledInt = 1
	}

	if exists {
		// Обновляем существующую запись
		switch tip {
		case "email":
			_, err = tx.ExecContext(ctx, `UPDATE notifications SET Email = ? WHERE UserId = ?`, enabledInt, userId)
		case "telega":
			if telegaId != 0 {
				_, err = tx.ExecContext(ctx, `UPDATE notifications SET Telegram_enabled = ?, Telegram = ? WHERE UserId = ?`,
					enabledInt, telegaId, userId)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE notifications SET Telegram_enabled = ? WHERE UserId = ?`,
					enabledInt, userId)
			}
		case "instant":
			_, err = tx.ExecContext(ctx, `UPDATE notifications SET Instant = ? WHERE UserId = ?`, enabledInt, userId)
		default:
			return fmt.Errorf("неизвестный тип уведомления: %s", tip)
		}
	} else {
		// Создаём новую запись
		switch tip {
		case "email":
			_, err = tx.ExecContext(ctx, `INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant) VALUES (?, ?, 0, 0, 0)`,
				userId, enabledInt)
		case "telega":
			telegaValue := sql.NullInt64{Int64: int64(telegaId), Valid: telegaId != 0}
			_, err = tx.ExecContext(ctx, `INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant) VALUES (?, 0, ?, ?, 0)`,
				userId, telegaValue, enabledInt)
		case "instant":
			_, err = tx.ExecContext(ctx, `INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant) VALUES (?, 0, 0, 0, ?)`,
				userId, enabledInt)
		default:
			return fmt.Errorf("неизвестный тип уведомления: %s", tip)
		}
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при обновлении уведомления: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка обновления уведомления: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) GetNotificationsData(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Получаем email пользователя
	var userEmail sql.NullString
	err := d.Conn().QueryRowContext(ctx, "SELECT Email FROM user_auth WHERE UserId = ? LIMIT 1", userId).Scan(&userEmail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении email пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка получения email пользователя: %w", err)
		}
	}

	// Проверяем существование записи в notifications
	var found bool
	err = d.Conn().QueryRowContext(ctx, "SELECT COUNT(*) > 0 FROM notifications WHERE UserId = ?", userId).Scan(&found)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при проверке существования уведомлений: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка проверки существования уведомлений: %w", err)
		}
	}

	var result []byte

	if found {
		// Формируем JSON с данными уведомлений
		query := `
   SELECT JSON_OBJECT(
    'email', JSON_OBJECT(
     'data', ?,
     'enabled', Email = 1
    ),
    'telega', JSON_OBJECT(
     'data', IF(Telegram = 0, NULL, CAST(Telegram AS CHAR)),
     'enabled', Telegram_enabled = 1
    ),
    'instant', JSON_OBJECT(
     'enabled', Instant = 1
    ),
    'events', JSON_OBJECT(
     'start', Start = 1,
     'end', End = 1,
     'target', Target = 1
    )
   )
   FROM notifications
   WHERE UserId = ?`

		err = d.Conn().QueryRowContext(ctx, query, userEmail.String, userId).Scan(&result)
	} else {
		// Возвращаем структуру по умолчанию
		query := `
   SELECT JSON_OBJECT(
    'email', JSON_OBJECT(
     'data', ?,
     'enabled', false
    ),
    'telega', JSON_OBJECT(
     'data', NULL,
     'enabled', false
    ),
    'instant', JSON_OBJECT(
     'enabled', false
    ),
    'events', JSON_OBJECT(
     'start', false,
     'end', false,
     'target', false
    )
   )`

		err = d.Conn().QueryRowContext(ctx, query, userEmail.String).Scan(&result)
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении данных уведомлений: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil
		default:
			return nil, fmt.Errorf("ошибка получения данных уведомлений: %w", err)
		}
	}

	if len(result) == 0 {
		return nil, nil
	}

	return result, nil
}

func (d *DB) SaveNotificationEvent(userId uint32, start, end, target bool) error {
	// Проверяем входное значение
	if userId == 0 {
		return fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в SaveNotificationEvent: %v", rbErr, userId)
		}
	}()

	// Проверяем существование записи для данного UserId
	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM notifications WHERE UserId = ?)", userId).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ошибка проверки существования записи: %w", err)
	}

	if exists {
		// Обновляем существующую запись
		_, err = tx.ExecContext(ctx, `
   UPDATE notifications
   SET Start = ?, End = ?, Target = ?
   WHERE UserId = ?`,
			start, end, target, userId)
	} else {
		// Создаём новую запись
		_, err = tx.ExecContext(ctx, `
   INSERT INTO notifications (UserId, Start, End, Target)
   VALUES (?, ?, ?, ?)`,
			userId, start, end, target)
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении события уведомления: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения события уведомления: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) DeleteNotificationsChannel(userId uint32, chanelName string) error {
	// Проверяем входные значения
	if userId == 0 || chanelName == "" {
		return fmt.Errorf("получены некорректные значения: userId или chanelName пусты")
	}

	// Приводим название канала к нижнему регистру для унификации
	chanelName = strings.ToLower(chanelName)

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в DeleteNotificationsChannel: %v", rbErr, userId)
		}
	}()

	// Обновляем соответствующий канал в зависимости от типа
	switch chanelName {
	case "email":
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET Email = 0 WHERE UserId = ?", userId)
	case "telegram":
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET Telegram = NULL, Telegram_enabled = 0 WHERE UserId = ?", userId)
	case "instant":
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET Instant = 0 WHERE UserId = ?", userId)
	default:
		return fmt.Errorf("неизвестный тип канала: %s", chanelName)
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении канала уведомлений: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления канала уведомлений: %w", err)
		}
	}

	// Если все каналы отключены, сбрасываем флаги событий
	_, err = tx.ExecContext(ctx, `
  UPDATE notifications
  SET Start = 0, End = 0, Target = 0
  WHERE UserId = ?
    AND (Email = 0 OR Email IS NULL)
    AND (Telegram = 0 OR Telegram IS NULL)
    AND Instant = 0`, userId)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сбросе флагов событий: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена при сбросе флагов: %w", err)
		default:
			return fmt.Errorf("ошибка сброса флагов событий: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) GetUserDialogs(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос напрямую
	query := `
  SELECT JSON_ARRAYAGG(
   JSON_OBJECT(
    'DialogId', d.Id,
    'Date', d.Date,
    'Type', ct.Name,
    'Responder', r.Name,
    'Target', d.Target,
    'Trigger', d.` + "`Trigger`" + `
   )
  ) AS dialogData
  FROM dialogs d
  JOIN users u ON d.User = u.Id
  JOIN responders r ON d.Responder = r.Id
  JOIN chat_type ct ON d.Type = ct.Id
  WHERE d.User = ?`

	// Выполняем запрос
	var data sql.NullString
	err := d.Conn().QueryRowContext(ctx, query, userId).Scan(&data)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении диалогов пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil // Диалоги не найдены, но это не ошибка
		default:
			return nil, fmt.Errorf("ошибка получения диалогов пользователя: %w", err)
		}
	}

	// Если пользователя или диалогов нет, то null
	if !data.Valid {
		return nil, nil // Возвращаем nil для пустых данных
	}

	return json.RawMessage(data.String), nil
}

func (d *DB) GetDevUserData(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Сначала проверяем роль пользователя
	var userRole int
	err := d.Conn().QueryRowContext(ctx, "SELECT RoleId FROM users WHERE Id = ?", userId).Scan(&userRole)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при проверке роли пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("пользователь не найден")
		default:
			return nil, fmt.Errorf("ошибка проверки роли пользователя: %w", err)
		}
	}

	// Если роль не 0, возвращаем пустой JSON объект
	if userRole != 0 {
		return json.RawMessage("{}"), nil
	}

	// SQL запрос для получения данных разработчика
	query := `
  SELECT JSON_OBJECT(
  'Name', u.Name,
  'Email', ua.Email,
  'TimeZone', u.TimeZone,
  'Balance', u.balance,
  'GptModels', (
    SELECT JSON_ARRAYAGG(
      JSON_OBJECT(
        'Id', ug.Id,
        'Name', ug.Name,
        'Model', gm.Name,
        'Provider', mp.Name,
        'IsActive', IF(um.IsActive = 1, true, false)
      )
    )
    FROM user_models um
    LEFT JOIN user_gpt ug ON um.ModelId = ug.Id
    LEFT JOIN gpt_models gm ON ug.Model = gm.Id
    LEFT JOIN model_providers mp ON um.Provider = mp.Id
    WHERE um.UserId = u.Id
  ),
  'AvailableProviders', (
    SELECT JSON_ARRAYAGG(
      JSON_OBJECT(
        'provider', p.Name,
        'models', (
          SELECT JSON_ARRAYAGG(
            JSON_OBJECT(
              'id', m2.Id,
              'name', m2.Name
            )
          )
          FROM gpt_models m2
          WHERE m2.Provider = p.Id
        ),
        'default_model', (
          SELECT JSON_OBJECT(
            'id', m3.Id,
            'name', m3.Name
          )
          FROM gpt_models m3
          WHERE m3.Provider = p.Id AND m3.IsDefault = 1
          LIMIT 1
        )
      )
    )
    FROM model_providers p
  )
) AS json_result
FROM users u
LEFT JOIN user_auth ua ON ua.UserId = u.Id
WHERE u.Id = ?`

	// Выполняем запрос
	var result []byte
	err = d.Conn().QueryRowContext(ctx, query, userId).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении данных разработчика: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("данные пользователя не найдены")
		default:
			return nil, fmt.Errorf("ошибка получения данных разработчика: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от GetDevUserData")
	}

	return result, nil
}

func (d *DB) UpdateDevData(userId uint32, name, encEmail, emailHMAC, sha string) error {
	// Проверяем входное значение userId
	if userId == 0 {
		return fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в UpdateDevData: %v", rbErr, userId)
		}
	}()

	// Обновляем Users.Name, если Name не пустое
	if name != "" {
		_, err = tx.ExecContext(ctx, "UPDATE users SET Name = ? WHERE Id = ?", name, userId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при обновлении имени пользователя: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при обновлении имени: %w", err)
			default:
				return fmt.Errorf("ошибка обновления имени пользователя: %w", err)
			}
		}
	}

	// Обновляем user_auth.Email и EmailHash, если encEmail не пустое
	if encEmail != "" {
		_, err = tx.ExecContext(ctx,
			"UPDATE user_auth SET Email = ?, EmailHash = ? WHERE UserId = ?",
			encEmail, emailHMAC, userId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при обновлении email: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при обновлении email: %w", err)
			default:
				return fmt.Errorf("ошибка обновления email: %w", err)
			}
		}
	}

	// Обновляем user_auth.SHA, если SHA не пустое
	if sha != "" {
		_, err = tx.ExecContext(ctx, "UPDATE user_auth SET SHA = ? WHERE UserId = ?", sha, userId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при обновлении пароля: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при обновлении пароля: %w", err)
			default:
				return fmt.Errorf("ошибка обновления пароля: %w", err)
			}
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) UpdateDevGPTModel(provider string, modId uint8) error {
	// Проверяем входные данные
	if provider == "" {
		return fmt.Errorf("получен пустой provider")
	}
	if modId == 0 {
		return fmt.Errorf("получен некорректный modId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в UpdateDevGPTModel: %v", rbErr)
		}
	}()

	// Получаем ID провайдера по имени
	var providerId int
	err = tx.QueryRowContext(ctx,
		"SELECT Id FROM model_providers WHERE Name = ?",
		provider).Scan(&providerId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("провайдер '%s' не найден", provider)
		}
		return fmt.Errorf("ошибка получения ID провайдера: %w", err)
	}

	// Проверяем, что модель существует и принадлежит этому провайдеру
	var modelExists bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM gpt_models WHERE Id = ? AND Provider = ?)",
		modId, providerId).Scan(&modelExists)
	if err != nil {
		return fmt.Errorf("ошибка проверки существования модели: %w", err)
	}
	if !modelExists {
		return fmt.Errorf("модель с ID %d не найдена для провайдера '%s'", modId, provider)
	}

	// Сбрасываем IsDefault для всех моделей этого провайдера
	_, err = tx.ExecContext(ctx,
		"UPDATE gpt_models SET IsDefault = 0 WHERE Provider = ?",
		providerId)
	if err != nil {
		return fmt.Errorf("ошибка сброса флага IsDefault: %w", err)
	}

	// Устанавливаем IsDefault = 1 для выбранной модели
	result, err := tx.ExecContext(ctx,
		"UPDATE gpt_models SET IsDefault = 1 WHERE Id = ? AND Provider = ?",
		modId, providerId)
	if err != nil {
		return fmt.Errorf("ошибка установки модели по умолчанию: %w", err)
	}

	// Проверяем, была ли затронута хотя бы одна строка
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка получения количества затронутых строк: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("модель не была обновлена")
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	logger.Info("Модель по умолчанию обновлена: provider='%s', modelId=%d", provider, modId)
	return nil
}

func (d *DB) GetOrSetUserStorageLimit(userID uint32, setStorage int64) (remaining uint64, totalLimit uint64, err error) {
	// Проверяем входное значение
	if userID == 0 {
		return 0, 0, fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в GetOrSetUserStorageLimit: %v", rbErr, userID)
		}
	}()

	// Получаем текущие значения с блокировкой строки
	var vLimit, vUsed int64
	err = tx.QueryRowContext(ctx, `
  SELECT quota_bytes, used_bytes
  FROM user_storage_quota
  WHERE user_id = ?
  FOR UPDATE`, userID).Scan(&vLimit, &vUsed)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, 0, fmt.Errorf("тайм-аут (%d с) при получении лимитов хранилища: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, 0, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return 0, 0, fmt.Errorf("подписка пользователя не найдена")
		default:
			return 0, 0, fmt.Errorf("ошибка получения лимитов хранилища: %w", err)
		}
	}

	// Вычисляем новое значение занятости
	vNewUsed := vUsed + setStorage

	// Гарантируем границы: [0, StorageLimit]
	if vNewUsed < 0 {
		vNewUsed = 0
	} else if vNewUsed > vLimit {
		vNewUsed = vLimit
	}

	// Обновляем значение StorageUsed
	_, err = tx.ExecContext(ctx, `
  UPDATE user_storage_quota
  SET used_bytes = ?
  WHERE user_id = ?`, vNewUsed, userID)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, 0, fmt.Errorf("тайм-аут (%d с) при обновлении использования хранилища: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, 0, fmt.Errorf("ошибка обновления использования хранилища: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	// Вычисляем оставшееся место и возвращаем результат
	remaining = uint64(vLimit - vNewUsed)
	totalLimit = uint64(vLimit)

	return remaining, totalLimit, nil
}

func (d *DB) GetTypesGPT(provider commdom.ProviderType, modelType commdom.ModelType) (json.RawMessage, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос напрямую
	general := `
  SELECT JSON_ARRAYAGG(
   JSON_OBJECT(
    'Id', gm.Id,
    'Name', gm.Name
   )
  ) AS json_result
  FROM gpt_models gm WHERE Provider = ?`

	realtime := `
  SELECT JSON_ARRAYAGG(
   JSON_OBJECT(
    'Id', gm.Id,
    'Name', gm.Name
   )
  ) AS json_result
  FROM realtime_models gm WHERE Provider = ?`

	query := general
	if modelType == commdom.RealTime {
		query = realtime
	}

	// Выполняем запрос
	var result []byte
	err := d.Conn().QueryRowContext(ctx, query, provider).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при вызове функции GetTypesGPT: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("типы GPT не найдены")
		default:
			return nil, fmt.Errorf("ошибка вызова хранимой функции GetTypesGPT: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от GetTypesGPT")
	}

	return result, nil
}

func (d *DB) UserInfo(userID uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userID == 0 {
		return nil, fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос напрямую
	query := `
  SELECT JSON_OBJECT(
   'Date', u.Date,
   'Name', u.Name,
   'Role', u.RoleId,
   'Lang', u.Lang,
   'TimeZone', u.TimeZone,
   'AvailibleLang', (
    SELECT JSON_ARRAYAGG(
     JSON_OBJECT(
      'id', l.Id,
      'name', l.Code
     )
    )
    FROM languages l
   ),
   'Balance', u.balance,
   'Currency', u.currency,
   'AvailibleCurrency', (
    SELECT JSON_ARRAYAGG(
     JSON_OBJECT(
      'id', c.Id,
      'name', c.Name
     )
    )
    FROM currency c
   ),
   'TimeChange', u.Timechange,
   'GPTName', ug.Name,
   'Email', ua.Email,
   'Subscription', JSON_OBJECT(
    'StartDate', s.StartDate,
    'EndDate', s.EndDate,
    'MonthsPaid', s.MonthsPaid,
    'TotalCost', s.TotalCost,
    'Discount', s.Discount,
 	'StorageLimit', us.quota_bytes,
 	'StorageUsed', us.used_bytes
   ),
   'Notifications', JSON_OBJECT(
    'Email', IFNULL(n.Email = 1, false),
    'TgBotIsSet', n.Telegram IS NOT NULL AND n.Telegram <> 0,
    'Instant', IFNULL(n.Instant = 1, false),
    'TelegramEnabled', IFNULL(n.Telegram_enabled = 1, false),
    'Start', IFNULL(n.Start = 1, false),
    'End', IFNULL(n.End = 1, false),
    'Target', IFNULL(n.Target = 1, false)
   ),
   'ChannelsAvailable', JSON_OBJECT(
    'TgBotEnabled', IFNULL(ch.TgBot_enabled, 0),
    'WidgetEnabled', IFNULL(ch.Widget_enabled, 0),
    'TgUserBotEnabled', IFNULL(ch.TgUserBot_enabled, 0),
    'WhatsEnabled', IFNULL(ch.Whats_enabled, 0),
    'InstaEnabled', IFNULL(ch.Insta_enabled, 0)
   ),
   'TotpEnabled', ua.TOTPSecret IS NOT NULL,
   'MasterKey', ua.MasterKey IS NOT NULL
  ) AS userInfo
  FROM users u
  LEFT JOIN user_models um ON u.Id = um.UserId AND um.IsActive = 1
  LEFT JOIN user_gpt ug ON um.ModelId = ug.Id
  LEFT JOIN user_auth ua ON u.Id = ua.UserId
  LEFT JOIN subscriptions s ON u.Id = s.UserId
  LEFT JOIN user_storage_quota us ON u.Id = us.user_id
  LEFT JOIN notifications n ON u.Id = n.UserId
  LEFT JOIN channels ch ON u.Id = ch.UserId
  WHERE u.Id = ?`

	// Выполняем запрос
	var result []byte
	err := d.Conn().QueryRowContext(ctx, query, userID).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении информации о пользователе: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("информация о пользователе не найдена")
		default:
			return nil, fmt.Errorf("ошибка получения информации о пользователе: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от UserInfo")
	}

	return result, nil
}

func (d *DB) DeleteAllUserData(userID uint32) error {
	// Проверяем входное значение
	if userID == 0 {
		return fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Вызываем хранимую процедуру для удаления всех данных пользователя
	_, err := d.Conn().ExecContext(ctx, "CALL DeleteAllUserData(?)", userID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении всех данных пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления всех данных пользователя: %w", err)
		}
	}

	return nil
}

func (d *DB) OperatorsList(ctx context.Context, userID uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userID == 0 {
		return nil, fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	var result sql.NullString
	err := d.Conn().QueryRowContext(ctx, "SELECT Telegram FROM operators WHERE UserId = ?", userID).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении списка операторов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			// Возвращаем пустой JSON массив если нет записей
			return json.RawMessage("[]"), nil
		default:
			return nil, fmt.Errorf("ошибка получения списка операторов: %w", err)
		}
	}

	// Проверяем, было ли значение NULL или пустое
	if !result.Valid || result.String == "" {
		return json.RawMessage("[]"), nil
	}

	return json.RawMessage(result.String), nil
}

// SaveOperators сохраняет список операторов в таблицу operators через SP SaveOperators.
func (d *DB) SaveOperators(ctx context.Context, userID uint32, operatorType string, data json.RawMessage) error {
	// Проверяем входные значения
	if userID == 0 || operatorType == "" {
		return fmt.Errorf("получены некорректные значения: userID или operatorType пусты")
	}
	if len(data) == 0 || !json.Valid(data) {
		return fmt.Errorf("получены некорректные данные JSON")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	// Определяем SQL запрос в зависимости от типа оператора (защита от SQL injection)
	var query string
	switch operatorType {
	case "Telegram":
		query = "INSERT INTO operators (UserId, Telegram) VALUES (?, ?) ON DUPLICATE KEY UPDATE Telegram = ?, Timechange = NOW()"
	case "Widget":
		query = "INSERT INTO operators (UserId, Widget) VALUES (?, ?) ON DUPLICATE KEY UPDATE Widget = ?, Timechange = NOW()"
	default:
		return fmt.Errorf("неподдерживаемый тип оператора: %s", operatorType)
	}

	// Выполняем запрос
	_, err := d.Conn().ExecContext(ctx, query, userID, string(data), string(data))
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении операторов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения операторов: %w", err)
		}
	}

	return nil
}

func (d *DB) SaveUserTimeZone(userID uint32, timeZone string) error {
	// Проверяем входные значения
	if userID == 0 || timeZone == "" {
		return fmt.Errorf("получены некорректные значения: userId или timeZone пусты")
	}
	if len(timeZone) == 0 || len(timeZone) > 64 {
		return fmt.Errorf("получены некорректные данные timeZone")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx, "UPDATE users SET TimeZone = ? WHERE Id = ?", timeZone, userID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении часового пояса: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения часового пояса: %w", err)
		}
	}

	return nil
}

func (d *DB) SaveUserLanguage(userID uint32, language string) error {
	// Проверяем входные значения
	if userID == 0 || language == "" {
		return fmt.Errorf("получены некорректные значения: userId или language пусты")
	}
	if len(language) != 2 {
		return fmt.Errorf("получены некорректные данные language")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"UPDATE users SET lang = (SELECT id FROM languages WHERE Code=?) WHERE users.Id=?", language, userID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении языка пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения языка пользователя: %w", err)
		}
	}

	return nil
}

// ============================================================================
// Методы для работы с multi-model системой
// ============================================================================
// CheckActiveChannels возвращает true если у пользователя хотя бы один канал активен.
func (d *DB) CheckActiveChannels(userId uint32) (bool, error) {
	if userId == 0 {
		return false, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var active bool
	err := d.Conn().QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM channels
			WHERE UserId = ?
			  AND (TgBot_enabled = 1
			    OR Widget_enabled = 1
			    OR TgUserBot_enabled = 1
			    OR Whats_enabled = 1
			    OR Insta_enabled = 1
			    OR Avito_enabled = 1)
		)`, userId).Scan(&active)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return false, fmt.Errorf("тайм-аут (%d с) при проверке активных каналов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return false, fmt.Errorf("операция отменена: %w", err)
		default:
			return false, fmt.Errorf("ошибка проверки активных каналов: %w", err)
		}
	}
	return active, nil
}

// GetActiveChannels возвращает список имён активных каналов пользователя.
func (d *DB) GetActiveChannels(userId uint32) ([]string, error) {
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var tg, wid, tgu, wa, inst, av sql.NullBool
	err := d.Conn().QueryRowContext(ctx,
		`SELECT TgBot_enabled, Widget_enabled, TgUserBot_enabled, Whats_enabled, Insta_enabled, Avito_enabled
		 FROM channels WHERE UserId = ? LIMIT 1`, userId).
		Scan(&tg, &wid, &tgu, &wa, &inst, &av)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("ошибка получения активных каналов: %w", err)
	}

	var channels []string
	if tg.Valid && tg.Bool {
		channels = append(channels, "tgbot")
	}
	if wid.Valid && wid.Bool {
		channels = append(channels, "widget")
	}
	if tgu.Valid && tgu.Bool {
		channels = append(channels, "tguserbot")
	}
	if wa.Valid && wa.Bool {
		channels = append(channels, "whatsbot")
	}
	if inst.Valid && inst.Bool {
		channels = append(channels, "insta")
	}
	if av.Valid && av.Bool {
		channels = append(channels, "avito")
	}
	return channels, nil
}

// ServiceList возвращает список типов сервисов, подключённых к пользователю.
func (d *DB) ServiceList(userId uint32) ([]string, error) {
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT ServiceType FROM service WHERE UserId = ? ORDER BY ServiceType", userId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении списка сервисов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка получения списка сервисов: %w", err)
		}
	}
	defer func() { _ = rows.Close() }()

	var services []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, fmt.Errorf("ошибка сканирования сервиса: %w", err)
		}
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации сервисов: %w", err)
	}
	return services, nil
}

// AddService добавляет тип сервиса пользователю (игнорирует дубликаты).
func (d *DB) AddService(userId uint32, serviceType string) error {
	if userId == 0 || serviceType == "" {
		return fmt.Errorf("получены некорректные значения")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"INSERT IGNORE INTO service (UserId, ServiceType) VALUES (?, ?)", userId, serviceType)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при добавлении сервиса: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка добавления сервиса: %w", err)
		}
	}
	return nil
}

// DeleteService удаляет тип сервиса у пользователя.
func (d *DB) DeleteService(userId uint32, serviceType string) error {
	if userId == 0 || serviceType == "" {
		return fmt.Errorf("получены некорректные значения")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"DELETE FROM service WHERE UserId = ? AND ServiceType = ?", userId, serviceType)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении сервиса: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления сервиса: %w", err)
		}
	}
	return nil
}

// CheckDemo проверяет, является ли пользователь демо-пользователем (RoleId=1).
func (d *DB) CheckDemo(userId uint32) (bool, error) {
	if userId == 0 {
		return false, fmt.Errorf("получен пустой userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var roleId uint8
	err := d.Conn().QueryRowContext(ctx, "SELECT RoleId FROM users WHERE Id = ?", userId).Scan(&roleId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return false, fmt.Errorf("тайм-аут (%d с) при проверке демо-роли: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return false, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return false, fmt.Errorf("пользователь с Id=%d не найден", userId)
		default:
			return false, fmt.Errorf("ошибка проверки демо-роли: %w", err)
		}
	}
	return roleId == 1, nil
}

// GetUsersWithGoogleToken возвращает список userId всех пользователей,
// у которых есть активная запись в google_oauth_tokens.
func (d *DB) GetUsersWithGoogleToken() ([]uint32, error) {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT user_id FROM google_oauth_tokens ORDER BY user_id")
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении пользователей с Google токенами: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка получения пользователей с Google токенами: %w", err)
		}
	}
	defer func() { _ = rows.Close() }()

	var users []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("ошибка сканирования userId: %w", err)
		}
		users = append(users, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации по строкам: %w", err)
	}
	return users, nil
}

// GetMigratedUsersEmails возвращает зашифрованные email всех мигрированных пользователей.
func (d *DB) GetMigratedUsersEmails() ([]struct {
	UserId   uint32
	EncEmail string
}, error) {
	ctx, cancel := context.WithTimeout(d.Context(), 30*time.Second)
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT UserId, Email FROM user_auth WHERE EmailHash IS NOT NULL")
	if err != nil {
		return nil, fmt.Errorf("ошибка получения мигрированных пользователей: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []struct {
		UserId   uint32
		EncEmail string
	}
	for rows.Next() {
		var item struct {
			UserId   uint32
			EncEmail string
		}
		if err := rows.Scan(&item.UserId, &item.EncEmail); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UsersWithoutSubscription находит пользователей у которых истекла
func (d *DB) UsersWithoutSubscription() ([]uint32, error) {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	query := `
		SELECT u.Id
FROM users u
JOIN user_auth a ON a.UserId = u.Id
JOIN subscriptions s
  ON u.Id = s.UserId AND s.Notified = FALSE AND s.EndDate < CURRENT_DATE()
WHERE u.RoleId = 2 AND a.Disabled = 0;
	`
	rows, err := d.Conn().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("ошибка при поиске пользователей без подписки: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var userIds []uint32
	for rows.Next() {
		var userId uint32
		if err := rows.Scan(&userId); err != nil {
			return nil, fmt.Errorf("ошибка сканирования userId: %w", err)
		}
		userIds = append(userIds, userId)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации по строкам: %w", err)
	}

	return userIds, nil
}

func (d *DB) SetUsersSubscriptionNotified(users []uint32) error {
	if len(users) == 0 {
		return nil // Нет пользователей для обновления
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Создаем строку с плейсхолдерами для IN (?, ?, ...)
	placeholders := strings.Repeat("?,", len(users))
	placeholders = placeholders[:len(placeholders)-1] // Удаляем последнюю запятую

	query := fmt.Sprintf("UPDATE subscriptions SET Notified = TRUE WHERE UserId IN (%s)", placeholders)

	// Преобразуем []uint32 в []any для передачи в ExecContext
	args := make([]any, len(users))
	for i, v := range users {
		args[i] = v
	}

	_, err := d.Conn().ExecContext(ctx, query, args...)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при обновлении статуса уведомления: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка при обновлении статуса уведомления: %w", err)
		}
	}

	return nil
}
