package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ikermy/air-common/pkg/comdom"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

func (d *DB) FastCheckActiveUserModel(userID uint32) (bool, error) {
	if userID == 0 {
		return false, fmt.Errorf("неверный userID")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	query := `
	SELECT EXISTS(
    SELECT 1 
    FROM user_models 
    WHERE UserId = ? AND IsActive = 1
	) AS HasActive`

	var hay bool
	scanErr := d.Conn().QueryRowContext(ctx, query, userID).Scan(&hay)
	if scanErr != nil {
		switch {
		case errors.Is(scanErr, context.DeadlineExceeded):
			return false, fmt.Errorf("тайм-аут (%d с) при получении активной модели: %w", mode.GetSQLTimeToCancel(), scanErr)
		case errors.Is(scanErr, context.Canceled):
			return false, fmt.Errorf("операция отменена: %w", scanErr)
		default:
			return false, fmt.Errorf("ошибка получения активной модели: %w", scanErr)
		}
	}

	return hay, nil
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

func (d *DB) GetTypesGPT(provider comdom.ProviderType, modelType comdom.ModelType) (json.RawMessage, error) {
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
	if modelType == comdom.RealTime {
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
