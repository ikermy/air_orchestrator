package mcp

import (
	"strings"
	"testing"

	"github.com/ikermy/air-common/pkg/comdom"
)

// ---- minimal mocks ----

type mockModelStore struct {
	models []comdom.UniversalModelData
	err    error
}

func (m *mockModelStore) GetUserModels(_ uint32) ([]comdom.UniversalModelData, error) {
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
