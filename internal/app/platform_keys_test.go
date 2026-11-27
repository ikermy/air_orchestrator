package app

import (
	"testing"

	"github.com/ikermy/air-common/v2/pkg/comdom"
)

func TestPlatformKeysFromEnv(t *testing.T) {
	t.Setenv("AIR_PLATFORM_KEY_OPENAI", "sk-openai")
	t.Setenv("AIR_PLATFORM_KEY_MISTRAL", "  ")
	t.Setenv("AIR_PLATFORM_KEY_GOOGLE", "google-key")
	t.Setenv("AIR_PLATFORM_KEY_ELEVENLABS", "el-key")

	keys := platformKeysFromEnv()
	if keys[comdom.ProviderOpenAI] != "sk-openai" {
		t.Fatalf("openai = %q", keys[comdom.ProviderOpenAI])
	}
	if _, ok := keys[comdom.ProviderMistral]; ok {
		t.Fatal("empty mistral key must be ignored")
	}
	if keys[comdom.ProviderGoogle] != "google-key" {
		t.Fatalf("google = %q", keys[comdom.ProviderGoogle])
	}
	if keys[comdom.ProviderElevenLabs] != "el-key" {
		t.Fatalf("elevenlabs = %q", keys[comdom.ProviderElevenLabs])
	}
}
