package db

import (
	"context"
	"encoding/json"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/ikermy/air-common/pkg/comdom"
	"github.com/ikermy/air-logger/v2/pkg/logger"

	"testing"
)

func initDB(t *testing.T) (*DB, context.CancelFunc) {
	t.Helper()

	// Инициализируем логгер для тестов
	logger.StdOut().WithLogLevel(logger.DEBUG).Apply()

	ctx, cancel := context.WithCancel(context.Background())
	db, err := New(ctx)
	if err != nil {
		cancel()
		t.Fatalf("Ошибка подключения к БД: %v", err)
	}
	return db, cancel
}

func TestGetUserDetales(t *testing.T) {
	db, cancel := initDB(t)
	defer cancel()
	defer db.Close()

	rawjson, err := db.GetUserDetails(23)
	if err != nil {
		t.Fatalf("SetOrGetDemoUser failed: %v", err)
	}

	fmt.Println(string(rawjson))
	// Проверяю что "RoleName" = "Developer"
	var userDetails struct {
		RoleName string `json:"RoleName"`
	}
	err = json.Unmarshal(rawjson, &userDetails)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if userDetails.RoleName != "Developer" {
		t.Fatalf("Expected RoleName to be 'Developer', got '%s'", userDetails.RoleName)
	}

	t.Logf("User details for userId %d: %s", 23, string(rawjson))
}

func TestGetAllUserModels(t *testing.T) {
	db, cancel := initDB(t)
	defer cancel()
	defer db.Close()

	const testUserId = uint32(23)

	models, err := db.GetAllUserModels(testUserId)
	if err != nil {
		t.Fatalf("GetAllUserModels failed: %v", err)
	}

	if len(models) == 0 {
		t.Logf("No models found for userId %d", testUserId)
		return
	}

	t.Logf("Found %d model(s) for userId %d:", len(models), testUserId)
	for i, model := range models {
		t.Logf("  Model %d:", i+1)
		t.Logf("    ModelId: %d", model.ModelId)
		t.Logf("    Provider: %d", model.Provider)
		t.Logf("    IsActive: %v", model.IsActive)
		t.Logf("    AssistId: %s", model.AssistId)
		t.Logf("    FileIds count: %d", len(model.FileIds))

		// Проверяем, что основные поля заполнены
		if model.ModelId == 0 {
			t.Errorf("Model %d has empty ModelId", i+1)
		}
		if model.AssistId == "" {
			t.Errorf("Model %d has empty AssistId", i+1)
		}
	}

	// Проверяем, что есть хотя бы одна активная модель
	hasActive := false
	for _, model := range models {
		if model.IsActive {
			hasActive = true
			break
		}
	}
	if !hasActive {
		t.Logf("Warning: No active model found for userId %d", testUserId)
	}
}

func TestGetActiveModel(t *testing.T) {
	db, cancel := initDB(t)
	defer cancel()
	defer db.Close()

	const testUserId = uint32(23)

	activeModel, err := db.GetActiveModel(testUserId)
	if err != nil {
		t.Fatalf("GetActiveModel failed: %v", err)
	}

	if activeModel == nil {
		t.Logf("No active model found for userId %d", testUserId)
		return
	}

	t.Logf("Active model for userId %d:", testUserId)
	t.Logf("  ModelId: %d", activeModel.ModelId)
	t.Logf("  Provider: %d", activeModel.Provider)
	t.Logf("  IsActive: %v", activeModel.IsActive)
	t.Logf("  AssistId: %s", activeModel.AssistId)
	t.Logf("  FileIds count: %d", len(activeModel.FileIds))

	// Проверяем, что это действительно активная модель
	if !activeModel.IsActive {
		t.Errorf("Expected IsActive to be true, got false")
	}

	// Проверяем обязательные поля
	if activeModel.ModelId == 0 {
		t.Errorf("Active model has empty ModelId")
	}
	if activeModel.AssistId == "" {
		t.Errorf("Active model has empty AssistId")
	}
}

func TestGetModelByProvider(t *testing.T) {
	db, cancel := initDB(t)
	defer cancel()
	defer db.Close()

	const testUserId = uint32(23)

	// Тестируем для провайдера OpenAI (1)
	t.Run("OpenAI Provider", func(t *testing.T) {
		provider := comdom.ProviderOpenAI
		model, err := db.GetModelByProvider(testUserId, provider)
		if err != nil {
			t.Fatalf("GetModelByProvider(OpenAI) failed: %v", err)
		}

		if model == nil {
			t.Logf("No OpenAI model found for userId %d", testUserId)
			return
		}

		t.Logf("OpenAI model for userId %d:", testUserId)
		t.Logf("  ModelId: %d", model.ModelId)
		t.Logf("  Provider: %d", model.Provider)
		t.Logf("  IsActive: %v", model.IsActive)
		t.Logf("  AssistId: %s", model.AssistId)
		t.Logf("  FileIds count: %d", len(model.FileIds))

		// Проверяем, что провайдер совпадает
		if model.Provider != provider {
			t.Errorf("Expected provider %d, got %d", provider, model.Provider)
		}

		// Проверяем обязательные поля
		if model.ModelId == 0 {
			t.Errorf("Model has empty ModelId")
		}
		if model.AssistId == "" {
			t.Errorf("Model has empty AssistId")
		}

		// Проверяем формат AssistId для OpenAI (должен начинаться с "asst_")
		if len(model.AssistId) > 0 && model.AssistId[:5] != "asst_" {
			t.Errorf("Expected OpenAI AssistId to start with 'asst_', got: %s", model.AssistId)
		}
	})

	// Тестируем для провайдера Mistral (2)
	t.Run("Mistral Provider", func(t *testing.T) {
		provider := comdom.ProviderMistral
		model, err := db.GetModelByProvider(testUserId, provider)
		if err != nil {
			t.Fatalf("GetModelByProvider(Mistral) failed: %v", err)
		}

		if model == nil {
			t.Logf("No Mistral model found for userId %d", testUserId)
			return
		}

		t.Logf("Mistral model for userId %d:", testUserId)
		t.Logf("  ModelId: %d", model.ModelId)
		t.Logf("  Provider: %d", model.Provider)
		t.Logf("  IsActive: %v", model.IsActive)
		t.Logf("  AssistId: %s", model.AssistId)
		t.Logf("  FileIds count: %d", len(model.FileIds))

		// Проверяем, что провайдер совпадает
		if model.Provider != provider {
			t.Errorf("Expected provider %d, got %d", provider, model.Provider)
		}

		// Проверяем обязательные поля
		if model.ModelId == 0 {
			t.Errorf("Model has empty ModelId")
		}
		if model.AssistId == "" {
			t.Errorf("Model has empty AssistId")
		}

		// Проверяем формат AssistId для Mistral (должен начинаться с "ag_")
		if len(model.AssistId) > 0 && len(model.AssistId) >= 3 && model.AssistId[:3] != "ag_" {
			t.Logf("Warning: Mistral AssistId doesn't start with 'ag_': %s", model.AssistId)
		}
	})
}

func TestGetUserDialogs(t *testing.T) {
	db, cancel := initDB(t)
	defer cancel()
	defer db.Close()

	const testUserId = uint32(23)

	dialogsJSON, err := db.GetUserDialogs(testUserId)
	if err != nil {
		t.Fatalf("GetUserDialogs failed: %v", err)
	}

	if dialogsJSON == nil {
		t.Logf("No dialogs found for userId %d", testUserId)
		return
	}

	t.Logf("Dialogs JSON for userId %d: %s", testUserId, string(dialogsJSON))

	// Парсим JSON для проверки структуры
	var dialogs []struct {
		DialogId  int    `json:"DialogId"`
		Date      string `json:"Date"`
		Type      string `json:"Type"`
		Responder string `json:"Responder"`
		Target    int    `json:"Target"`
		Trigger   int    `json:"Trigger"`
	}
	err = json.Unmarshal(dialogsJSON, &dialogs)
	if err != nil {
		t.Fatalf("Failed to unmarshal dialogs JSON: %v", err)
	}

	if len(dialogs) == 0 {
		t.Logf("Parsed dialogs array is empty for userId %d", testUserId)
		return
	}

	t.Logf("Found %d dialog(s) for userId %d", len(dialogs), testUserId)

	// Проверяем структуру каждого диалога
	for i, dialog := range dialogs {
		t.Logf("Dialog %d:", i+1)
		t.Logf("  DialogId: %d", dialog.DialogId)
		t.Logf("  Date: %s", dialog.Date)
		t.Logf("  Type: %s", dialog.Type)
		t.Logf("  Responder: %s", dialog.Responder)
		t.Logf("  Target: %d", dialog.Target)
		t.Logf("  Trigger: %d", dialog.Trigger)

		// Проверяем обязательные поля
		if dialog.DialogId == 0 {
			t.Errorf("Dialog %d has invalid DialogId", i+1)
		}
		if dialog.Type == "" {
			t.Errorf("Dialog %d has empty Type", i+1)
		}
		if dialog.Responder == "" {
			t.Errorf("Dialog %d has empty Responder", i+1)
		}
	}
}

func TestGetActiveProvider(t *testing.T) {
	db, cancel := initDB(t)
	defer cancel()
	defer db.Close()

	const testUserId = uint32(23)

	provider, err := db.GetActiveProvider(testUserId)
	if err != nil {
		t.Fatalf("GetActiveProvider failed: %v", err)
	}

	// Проверяем, что провайдер корректен по типу
	if !provider.IsValid() {
		t.Fatalf("получен некорректный provider: %d", provider)
	}

	// Дополнительная проверка — ожидаемые провайдеры
	switch provider {
	case comdom.ProviderOpenAI, comdom.ProviderMistral:
		t.Logf("Active provider for user %d: %d", testUserId, provider)
	default:
		t.Fatalf("неизвестный провайдер: %d", provider)
	}
}

func BenchmarkGetActiveProviderComparison(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := New(ctx)
	if err != nil {
		b.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer db.Close()

	const testUserId = uint32(23)

	// Сбрасываем таймер, чтобы время подключения к БД не учитывалось
	b.ResetTimer()

	b.Run("OLD_WithTimeout", func(b *testing.B) {
		b.Skip("OLD_GetActiveProvider удалён из кода; исторический benchmark отключён")
	})

	b.Run("NEW_DirectContext", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := db.GetActiveProvider(testUserId)
			if err != nil {
				b.Fatalf("GetActiveProvider failed: %v", err)
			}
		}
	})
}
