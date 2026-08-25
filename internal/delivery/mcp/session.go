package mcp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ikermy/air-common/pkg/comdom"
)

// ParseSessionID разбирает заголовок X-Session-ID формата "userId:providerType".
// userId — реальный (не кодированный) uint32.
// providerType — числовое значение comdom.ProviderType.
func ParseSessionID(header string) (userId uint32, provider comdom.ProviderType, err error) {
	parts := strings.SplitN(header, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid X-Session-ID format, expected userId:providerType")
	}
	uid, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid userId: %w", err)
	}
	pt, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid providerType: %w", err)
	}
	return uint32(uid), comdom.ProviderType(pt), nil
}
