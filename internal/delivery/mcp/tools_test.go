package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"air_orchestrator/internal/config"

	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/comdom"
)

// ---- minimal mocks ----

type mockModelStore struct {
	models []comdom.UniversalModelData
	err    error
}

func (m *mockModelStore) GetUserModels(_ uint64) ([]comdom.UniversalModelData, error) {
	return m.models, m.err
}

// buildHandler создаёт Handler с минимальными зависимостями для unit-тестов,
// не требующих DB или conf.
func buildHandler(store *mockModelStore) *Handler {
	return &Handler{mod: store}
}

// ---- TestMCP_ToolsList ----

func TestMCP_ToolsList(t *testing.T) {
	store := &mockModelStore{
		models: []comdom.UniversalModelData{
			{
				Provider: comdom.ProviderType(1),
				S3:       true,
			},
		},
	}
	h := buildHandler(store)

	tools, err := h.buildToolsList(42, comdom.ProviderType(1))
	if err != nil {
		t.Fatalf("buildToolsList returned error: %v", err)
	}

	names := make(map[string]bool, len(tools))
	for _, tl := range tools {
		names[tl.Name] = true
	}

	for _, want := range []string{"get_s3_files", "create_file"} {
		if !names[want] {
			t.Errorf("tool %q отсутствует в tools/list; got %v", want, names)
		}
	}
}

// ---- TestMCP_ToolsList_GenerateMusic ----

func TestMCP_ToolsList_GenerateMusic(t *testing.T) {
	base := []comdom.UniversalModelData{
		{Provider: comdom.ProviderType(1), S3: true},
	}

	withMusic := buildHandler(&mockModelStore{models: []comdom.UniversalModelData{
		{Provider: comdom.ProviderType(1), S3: true, CreateMusic: true},
	}})
	tools, err := withMusic.buildToolsList(42, comdom.ProviderType(1))
	if err != nil {
		t.Fatalf("buildToolsList returned error: %v", err)
	}
	if !hasTool(tools, "generate_music") {
		t.Errorf("generate_music отсутствует при CreateMusic=true; got %v", toolNames(tools))
	}

	withoutMusic := buildHandler(&mockModelStore{models: base})
	tools, err = withoutMusic.buildToolsList(42, comdom.ProviderType(1))
	if err != nil {
		t.Fatalf("buildToolsList returned error: %v", err)
	}
	if hasTool(tools, "generate_music") {
		t.Errorf("generate_music виден при CreateMusic=false; got %v", toolNames(tools))
	}
}

func hasTool(tools []tool, name string) bool {
	for _, tl := range tools {
		if tl.Name == name {
			return true
		}
	}
	return false
}

func toolNames(tools []tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	return names
}

// ---- TestMCP_PromptsGet_System ----

func TestMCP_PromptsGet_System(t *testing.T) {
	store := &mockModelStore{
		models: []comdom.UniversalModelData{
			{
				Provider: comdom.ProviderType(1),
				S3:       true,
			},
		},
	}
	h := buildHandler(store)

	hint := h.buildSystemPromptHint(42, comdom.ProviderType(1))

	for _, substr := range []string{"get_s3_files", "create_file"} {
		if !strings.Contains(hint, substr) {
			t.Errorf("hint не содержит %q:\n%s", substr, hint)
		}
	}
}

// ---- TestMCP_PromptsGet_System_Music ----

func TestMCP_PromptsGet_System_Music(t *testing.T) {
	store := &mockModelStore{
		models: []comdom.UniversalModelData{
			{Provider: comdom.ProviderType(1), CreateMusic: true},
		},
	}
	h := buildHandler(store)

	hint := h.buildSystemPromptHint(42, comdom.ProviderType(1))
	if !strings.Contains(hint, "generate_music") {
		t.Errorf("hint не содержит generate_music:\n%s", hint)
	}
}

// mockStore satisfies the large Store interface via the embedded nil interface;
// only the methods used by these tests are implemented.
type mockStore struct {
	comdb.Exterior
	owns    bool
	ownsErr error
}

func (m *mockStore) GetAppConfig(context.Context, string) (string, error) { return "", nil }

func (m *mockStore) UserTimeZone(uint64) (string, error) { return "UTC", nil }

func (m *mockStore) WriteAuditLog(context.Context, uint64, string, json.RawMessage) error {
	return nil
}

func (m *mockStore) UserOwnsResponder(uint64, uint64) (bool, error) { return m.owns, m.ownsErr }

func TestMCP_CallTool_RejectsNotAllowedTool(t *testing.T) {
	h := &Handler{
		mod: &mockModelStore{models: []comdom.UniversalModelData{
			{Provider: comdom.ProviderType(1), S3: true},
		}},
		store: &mockStore{},
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "calendar_create",
		"arguments": map[string]any{},
	})
	res := h.callTool(context.Background(), params, 42, comdom.ProviderType(1))
	if !res.IsError {
		t.Fatalf("calendar_create должен быть запрещён при выключенном GOAuth.Calendar")
	}
	if !strings.Contains(res.Content[0].Text, "tool not allowed") {
		t.Fatalf("неожиданная ошибка: %s", res.Content[0].Text)
	}
}

func TestMCP_CallTool_AllowsListedTool(t *testing.T) {
	h := &Handler{
		mod: &mockModelStore{models: []comdom.UniversalModelData{
			{Provider: comdom.ProviderType(1), S3: true},
		}},
		store: &mockStore{},
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "get_current_time",
		"arguments": map[string]any{},
	})
	res := h.callTool(context.Background(), params, 42, comdom.ProviderType(1))
	if res.IsError {
		t.Fatalf("get_current_time должен быть доступен: %s", res.Content[0].Text)
	}
}

func TestMCP_ToolsList_IdempotencyKey(t *testing.T) {
	h := &Handler{mod: &mockModelStore{models: []comdom.UniversalModelData{
		{Provider: comdom.ProviderType(1), S3: true},
	}}}
	tools, err := h.buildToolsList(42, comdom.ProviderType(1))
	if err != nil {
		t.Fatalf("buildToolsList: %v", err)
	}

	for _, name := range []string{"create_file", "save_image"} {
		var tl *tool
		for i := range tools {
			if tools[i].Name == name {
				tl = &tools[i]
				break
			}
		}
		if tl == nil {
			t.Fatalf("tool %q not found", name)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tl.InputSchema, &schema); err != nil {
			t.Fatalf("%s schema: %v", name, err)
		}
		if _, ok := schema.Properties["idempotency_key"]; !ok {
			t.Fatalf("%s schema has no idempotency_key", name)
		}
	}
}

func TestMCP_CallTool_Idempotency(t *testing.T) {
	calls := 0
	h := &Handler{
		mod: &mockModelStore{models: []comdom.UniversalModelData{
			{Provider: comdom.ProviderType(1), MetaAction: "lead"},
		}},
		store: &mockStore{owns: true},
		leadTargetFn: func(context.Context, int64) error {
			calls++
			return nil
		},
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "lead_target",
		"arguments": map[string]any{"resp_id": 123, "idempotency_key": "abc"},
	})

	for i := 0; i < 2; i++ {
		if res := h.callTool(context.Background(), params, 42, comdom.ProviderType(1)); res.IsError {
			t.Fatalf("call %d: %s", i+1, res.Content[0].Text)
		}
	}
	if calls != 1 {
		t.Fatalf("idempotent call executed %d times, want 1", calls)
	}

	params, _ = json.Marshal(map[string]any{
		"name":      "lead_target",
		"arguments": map[string]any{"resp_id": 123, "idempotency_key": "def"},
	})
	if res := h.callTool(context.Background(), params, 42, comdom.ProviderType(1)); res.IsError {
		t.Fatalf("call with new key: %s", res.Content[0].Text)
	}
	if calls != 2 {
		t.Fatalf("new key executed %d times, want 2", calls)
	}
}

func TestMCP_CallTool_RateLimit(t *testing.T) {
	h := &Handler{
		mod: &mockModelStore{models: []comdom.UniversalModelData{
			{Provider: comdom.ProviderType(1)},
		}},
		store: &mockStore{},
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "get_current_time",
		"arguments": map[string]any{},
	})

	for i := 0; i < config.MCPToolRateBurst; i++ {
		if res := h.callTool(context.Background(), params, 42, comdom.ProviderType(1)); res.IsError {
			t.Fatalf("call %d must be allowed: %s", i+1, res.Content[0].Text)
		}
	}

	res := h.callTool(context.Background(), params, 42, comdom.ProviderType(1))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "rate limit exceeded") {
		t.Fatalf("expected rate limit error, got: %s", res.Content[0].Text)
	}
}

func TestMCP_LeadTarget_Ownership(t *testing.T) {
	build := func(owns bool) *Handler {
		return &Handler{
			mod: &mockModelStore{models: []comdom.UniversalModelData{
				{Provider: comdom.ProviderType(1), MetaAction: "lead"},
			}},
			store: &mockStore{owns: owns},
			leadTargetFn: func(context.Context, int64) error {
				return nil
			},
		}
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "lead_target",
		"arguments": map[string]any{"resp_id": 123},
	})

	if res := build(false).callTool(context.Background(), params, 42, comdom.ProviderType(1)); !res.IsError {
		t.Fatalf("lead_target должен быть отклонён для чужого респондента")
	}

	res := build(true).callTool(context.Background(), params, 42, comdom.ProviderType(1))
	if res.IsError {
		t.Fatalf("lead_target должен пройти для своего респондента: %s", res.Content[0].Text)
	}
}
