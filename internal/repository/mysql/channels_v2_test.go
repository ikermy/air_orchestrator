package mysql

import (
	"testing"

	"github.com/ikermy/air-common/v2/pkg/comdom"
)

func TestNormalizeChannelType(t *testing.T) {
	cases := map[string]string{
		"tgbot":           "tgbot",
		"widget":          "widget",
		"tguserbot":       "tguserbot",
		"tgubot":          "tguserbot",
		"TelegramUserBot": "tguserbot",
		"whatsbot":        "whatsbot",
		"whats":           "whatsbot",
		"insta":           "insta",
		"Instagram":       "insta",
		"avito":           "avito",
	}
	for in, want := range cases {
		got, err := normalizeChannelType(in)
		if err != nil {
			t.Fatalf("normalizeChannelType(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeChannelType(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := normalizeChannelType("unknown"); err == nil {
		t.Fatal("expected error for unknown channel type")
	}
}

func TestChannelCodesAreUnique(t *testing.T) {
	seen := make(map[uint16]string)
	for _, info := range comdom.ConfigurableChannels() {
		if prev, ok := seen[info.Code]; ok {
			t.Fatalf("channels %q and %q share code %d", prev, info.Slug, info.Code)
		}
		seen[info.Code] = info.Slug
	}
}
