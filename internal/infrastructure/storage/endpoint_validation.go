package storage

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ValidateExternalEndpoint validates an S3 endpoint before the application
// creates a client. It intentionally resolves DNS here so private and
// metadata addresses cannot be reached through an external configuration.
func ValidateExternalEndpoint(ctx context.Context, raw string, production bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return fmt.Errorf("invalid S3 endpoint")
	}

	if production && !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("S3 endpoint must use https")
	}

	if !strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") {
		return fmt.Errorf("unsupported S3 endpoint scheme")
	}

	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("S3 endpoint must not contain path or query")
	}

	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") || strings.EqualFold(host, "metadata.google.internal") {
		return fmt.Errorf("S3 endpoint host is not allowed")
	}

	if ip := net.ParseIP(host); ip != nil {
		if forbiddenIP(ip) {
			return fmt.Errorf("S3 endpoint resolves to a private address")
		}
		return nil
	}

	if strings.Contains(host, "..") || strings.Contains(host, "/") {
		return fmt.Errorf("invalid S3 endpoint hostname")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("cannot resolve S3 endpoint hostname")
	}
	for _, addr := range addrs {
		if forbiddenIP(addr.AsSlice()) {
			return fmt.Errorf("S3 endpoint resolves to a private address")
		}
	}

	return nil
}

func ValidateBucketName(bucket string) error {
	if len(bucket) < 3 || len(bucket) > 63 || strings.HasPrefix(bucket, ".") || strings.HasSuffix(bucket, ".") || strings.Contains(bucket, "..") {
		return fmt.Errorf("invalid S3 bucket name")
	}

	for _, r := range bucket {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid S3 bucket name")
	}

	return nil
}

func forbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.Equal(net.ParseIP("169.254.169.254"))
}
