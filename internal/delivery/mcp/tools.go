package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ikermy/air-common/pkg/comdom"
	"github.com/ikermy/air-common/pkg/google_services"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// ========== Типы инструментов ==========

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// ========== Список инструментов ==========

// buildToolsList возвращает инструменты, доступные пользователю,
// на основе флагов его модели (S3, GOAuth.Calendar, GOAuth.Sheets).
func (h *Handler) buildToolsList(userId uint32, provider comdom.ProviderType) ([]tool, error) {
	// get_current_time доступен всегда
	tools := []tool{
		{
			Name:        "get_current_time",
			Description: "Получить текущее время пользователя с учётом часового пояса",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
	}

	models, err := h.mod.GetUserModels(userId)
	if err != nil {
		logger.Error("MCP buildToolsList GetUserModels: %v", err, userId)
		return tools, nil // минимальный набор при ошибке
	}

	var modelData *comdom.UniversalModelData
	for i := range models {
		if models[i].Provider == provider {
			modelData = &models[i]
			break
		}
	}
	if modelData == nil {
		return tools, nil
	}

	// lead_target — только если у пользователя задан MetaAction (бот-цель)
	if modelData.MetaAction != "" {
		tools = append(tools, tool{
			Name:        "lead_target",
			Description: "Triggers when the dialog goal is achieved. Call this to confirm goal completion.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"resp_id":{"type":"integer","description":"Respondent ID (conversation session ID)"}},"required":["resp_id"]}`),
		})
	}

	if modelData.S3 {
		tools = append(tools,
			tool{
				Name:        "get_s3_files",
				Description: "Получить список URL файлов пользователя из S3",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
			},
			tool{
				Name:        "create_file",
				Description: "Создать текстовый файл и сохранить в S3",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"file_name":{"type":"string","description":"Имя файла с расширением"},"content":{"type":"string","description":"Содержимое файла (UTF-8)"}},"required":["file_name","content"]}`),
			},
			tool{
				Name:        "save_image",
				Description: "Сохранить изображение в S3 (base64)",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"image_data":{"type":"string","description":"base64-кодированное изображение"},"file_name":{"type":"string","description":"Имя файла (.jpg, .png)"}},"required":["image_data","file_name"]}`),
			},
		)
	}

	if modelData.GOAuth.Calendar {
		tools = append(tools,
			tool{
				Name:        "calendar_create",
				Description: "Создать событие в Google Calendar",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"description":{"type":"string"},"start_time":{"type":"string","description":"RFC3339"},"end_time":{"type":"string","description":"RFC3339"},"location":{"type":"string"},"attendees":{"type":"array","items":{"type":"string"}}},"required":["title","start_time","end_time"]}`),
			},
			tool{
				Name:        "calendar_list",
				Description: "Получить список событий из Google Calendar",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"time_min":{"type":"string","description":"RFC3339"},"time_max":{"type":"string","description":"RFC3339"},"max_results":{"type":"integer","default":10}},"required":[]}`),
			},
			tool{
				Name:        "calendar_delete",
				Description: "Удалить событие из Google Calendar",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"event_id":{"type":"string"}},"required":["event_id"]}`),
			},
			tool{
				Name:        "calendar_get",
				Description: "Получить событие по ID из Google Calendar",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"event_id":{"type":"string"}},"required":["event_id"]}`),
			},
		)
	}

	if modelData.GOAuth.Sheets {
		tools = append(tools,
			tool{
				Name:        "sheets_read",
				Description: "Прочитать данные из Google Sheets",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"spreadsheet_id":{"type":"string"},"range":{"type":"string","description":"Например Sheet1!A1:D10"}},"required":["spreadsheet_id","range"]}`),
			},
			tool{
				Name:        "sheets_write",
				Description: "Записать данные в Google Sheets",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"spreadsheet_id":{"type":"string"},"range":{"type":"string"},"values":{"type":"array","items":{"type":"array","items":{"type":"string"}}}},"required":["spreadsheet_id","range","values"]}`),
			},
			tool{
				Name:        "sheets_append",
				Description: "Добавить строки в Google Sheets",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"spreadsheet_id":{"type":"string"},"range":{"type":"string"},"values":{"type":"array","items":{"type":"array","items":{"type":"string"}}}},"required":["spreadsheet_id","range","values"]}`),
			},
		)
	}

	return tools, nil
}

// ========== Диспетчер вызовов ==========

func (h *Handler) callTool(ctx context.Context, params json.RawMessage, userId uint32) toolResult {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}

	switch p.Name {
	case "lead_target":
		return h.toolLeadTarget(ctx, p.Arguments)
	case "get_current_time":
		return h.toolGetCurrentTime(userId)
	case "get_s3_files":
		return h.toolGetS3Files(userId)
	case "create_file":
		return h.toolCreateFile(p.Arguments, userId)
	case "save_image":
		return h.toolSaveImage(p.Arguments, userId)
	case "calendar_create":
		return h.toolCalendarCreate(ctx, p.Arguments, userId)
	case "calendar_list":
		return h.toolCalendarList(ctx, p.Arguments, userId)
	case "calendar_delete":
		return h.toolCalendarDelete(ctx, p.Arguments, userId)
	case "calendar_get":
		return h.toolCalendarGet(ctx, p.Arguments, userId)
	case "sheets_read":
		return h.toolSheetsRead(ctx, p.Arguments, userId)
	case "sheets_write":
		return h.toolSheetsWrite(ctx, p.Arguments, userId)
	case "sheets_append":
		return h.toolSheetsAppend(ctx, p.Arguments, userId)
	default:
		return toolErr(fmt.Sprintf("tool not found: %s", p.Name))
	}
}

// ========== Вспомогательные функции ==========

func toolOK(text string) toolResult {
	return toolResult{Content: []toolContent{{Type: "text", Text: text}}, IsError: false}
}

func toolErr(msg string) toolResult {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}, IsError: true}
}

func (h *Handler) calendarSvc(ctx context.Context) *google_services.CalendarService {
	get := func(key string) string { v, _ := h.store.GetAppConfig(ctx, key); return v }
	return google_services.NewCalendarService(ctx, h.store,
		get("google_oauth.client_id"),
		get("google_oauth.client_secret"),
		get("google_oauth.redirect_uri"),
	)
}

func (h *Handler) sheetsSvc(ctx context.Context) *google_services.SheetsService {
	get := func(key string) string { v, _ := h.store.GetAppConfig(ctx, key); return v }
	return google_services.NewSheetsService(ctx, h.store,
		get("google_oauth.client_id"),
		get("google_oauth.client_secret"),
		get("google_oauth.redirect_uri"),
	)
}

// ========== Реализации инструментов ==========

func (h *Handler) toolLeadTarget(ctx context.Context, args json.RawMessage) toolResult {
	var p struct {
		RespId int64 `json:"resp_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.RespId == 0 {
		return toolErr("invalid params: resp_id (integer) required")
	}

	// Используем инжектированный колбэк (web.CallLeadTarget) если он установлен,
	if h.leadTargetFn != nil {
		if err := h.leadTargetFn(ctx, p.RespId); err != nil {
			logger.Warn("MCP lead_target: ошибка rid=%d: %v", p.RespId, err)
			return toolErr("lead_target failed: " + err.Error())
		}
	}

	b, _ := json.Marshal(map[string]any{"target": true, "resp_id": p.RespId})
	return toolOK(string(b))
}

func (h *Handler) toolGetCurrentTime(userId uint32) toolResult {
	timezone, err := h.store.UserTimeZone(userId)
	if err != nil {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	// Полный ответ с разными форматами времени, чтобы LLM мог выбрать нужные поля для своих целей (календарь, сравнение дат, отображение пользователю и т.д.)
	//result := map[string]any{
	//	"success":   true,
	//	"timestamp": now.Unix(),
	//	"rfc3339":   now.Format(time.RFC3339),
	//	"date":      now.Format("2006-01-02"),
	//	"time":      now.Format("15:04:05"),
	//	"timezone":  timezone,
	//	"weekday":   now.Weekday().String(),
	//	"year":      now.Year(),
	//	"month":     int(now.Month()),
	//	"day":       now.Day(),
	//	"hour":      now.Hour(),
	//	"minute":    now.Minute(),
	//	"second":    now.Second(),
	//	"formatted": now.Format("2006-01-02 15:04:05 MST"),
	//}

	// Возвращаем только необходимое — меньше токенов в контексте OpenAI
	result := map[string]any{
		"datetime": now.Format("2006-01-02 15:04:05"),
		"timezone": timezone,
		"rfc3339":  now.Format(time.RFC3339),
	}
	b, _ := json.Marshal(result)
	return toolOK(string(b))
}

func (h *Handler) toolGetS3Files(userId uint32) toolResult {
	if h.files != nil {
		files, err := h.files.ListFiles(h.ctx, userId)
		if err != nil {
			return toolErr("failed to list files")
		}
		b, _ := json.Marshal(files)
		return toolOK(string(b))
	}
	return toolErr("storage is not configured")
}

func (h *Handler) toolCreateFile(args json.RawMessage, userId uint32) toolResult {
	var p struct {
		FileName string `json:"file_name"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.FileName == "" {
		return toolErr("invalid params: file_name and content required")
	}
	if h.files != nil {
		file, err := h.files.CreateTextFile(h.ctx, userId, p.FileName, bytes.NewReader([]byte(p.Content)), int64(len(p.Content)))
		if err != nil {
			return toolErr("failed to create file")
		}
		b, _ := json.Marshal(map[string]string{"file_name": file.FileName, "url": file.URL, "key": file.Key, "type": "doc"})
		return toolOK(string(b))
	}
	return toolErr("storage is not configured")
}

func (h *Handler) toolSaveImage(args json.RawMessage, userId uint32) toolResult {
	var p struct {
		ImageData string `json:"image_data"`
		FileName  string `json:"file_name"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.ImageData == "" || p.FileName == "" {
		return toolErr("invalid params: image_data and file_name required")
	}
	imageBytes, err := base64.StdEncoding.DecodeString(p.ImageData)
	if err != nil {
		imageBytes, err = base64.RawStdEncoding.DecodeString(p.ImageData)
		if err != nil {
			return toolErr("invalid base64 image_data")
		}
	}
	if h.files != nil {
		contentType := "application/octet-stream"
		switch strings.ToLower(filepath.Ext(p.FileName)) {
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".png":
			contentType = "image/png"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		}
		file, err := h.files.SaveImage(h.ctx, userId, p.FileName, bytes.NewReader(imageBytes), int64(len(imageBytes)), contentType)
		if err != nil {
			return toolErr("failed to save image")
		}
		b, _ := json.Marshal(map[string]string{"url": file.URL, "file_name": file.FileName, "key": file.Key})
		return toolOK(string(b))
	}
	return toolErr("storage is not configured")
}

func (h *Handler) toolCalendarCreate(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.CreateEventParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	result, err := h.calendarSvc(ctx).CreateEvent(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

func (h *Handler) toolCalendarList(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.ListEventsParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	if p.MaxResults == 0 {
		p.MaxResults = 10
	}
	result, err := h.calendarSvc(ctx).ListEvents(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

func (h *Handler) toolCalendarDelete(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.DeleteEventParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	result, err := h.calendarSvc(ctx).DeleteEvent(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

func (h *Handler) toolCalendarGet(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.GetEventParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	result, err := h.calendarSvc(ctx).GetEvent(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

func (h *Handler) toolSheetsRead(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.ReadRangeParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	result, err := h.sheetsSvc(ctx).ReadRange(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

func (h *Handler) toolSheetsWrite(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.WriteRangeParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	result, err := h.sheetsSvc(ctx).WriteRange(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

func (h *Handler) toolSheetsAppend(ctx context.Context, args json.RawMessage, userId uint32) toolResult {
	var p google_services.AppendRangeParams
	if err := json.Unmarshal(args, &p); err != nil {
		return toolErr("invalid params: " + err.Error())
	}
	p.UserID = userId
	result, err := h.sheetsSvc(ctx).AppendRange(p)
	if err != nil {
		return toolErr(err.Error())
	}
	return toolOK(result)
}

// ========== Промпт-хелперы ==========

// getUserModel возвращает модель пользователя для указанного провайдера.
// Возвращает nil если модель не найдена или произошла ошибка.
func (h *Handler) getUserModel(userId uint32, provider comdom.ProviderType) *comdom.UniversalModelData {
	models, err := h.mod.GetUserModels(userId)
	if err != nil {
		logger.Error("MCP getUserModel: %v", err, userId)
		return nil
	}
	for i := range models {
		if models[i].Provider == provider {
			return &models[i]
		}
	}
	return nil
}

// buildSystemPromptHint формирует компактные инструкции по использованию инструментов
// на основе флагов модели пользователя. Не содержит артефактов text-mode —
// используется одинаково для text и Realtime (голосового) режимов.
func (h *Handler) buildSystemPromptHint(userId uint32, provider comdom.ProviderType) string {
	m := h.getUserModel(userId, provider)

	var parts []string
	// get_current_time — только по необходимости, не перед каждым действием
	parts = append(parts, "Time: call get_current_time() ONLY when user asks about current time/date, or before calendar operations. Do NOT call it for file operations.")

	if m == nil {
		return strings.Join(parts, "\n")
	}

	if m.MetaAction != "" {
		parts = append(parts, fmt.Sprintf("Goal: %s", m.MetaAction))
	}

	if m.S3 {
		parts = append(parts,
			"Files: use get_s3_files() to list files, create_file() to create new ones.",
			"After create_file() — use the returned URL in your response. DO NOT invent URLs.",
		)
	}

	if m.Interpreter {
		parts = append(parts, "Code: use python tool for calculations and data processing only, NOT for creating user files.")
		if m.S3 {
			parts = append(parts, "File creation for user → create_file(), NOT python.")
		}
	}

	if m.GOAuth.Calendar {
		// Примечание: get_current_time() уже упомянут выше — не дублируем.
		parts = append(parts, "Calendar: use RFC3339+timezone for all date/time values in calendar operations.")
	}

	if m.GOAuth.Sheets {
		parts = append(parts,
			"Sheets: use sheets_read() to read spreadsheet data when user requests it.",
			"Table data → show in message text, do NOT create files from table data.",
		)
	}

	if m.WebSearch {
		parts = append(parts, "web: use web_search tool for current information.")
	}

	return strings.Join(parts, "\n")
}
