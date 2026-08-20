package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOStorage struct {
	client       *minio.Client
	publicClient *minio.Client
	accessKey    string
	secretKey    string
	bucket       string
}

func NewMinIOStorage(endpoint, accessKey, secretKey, bucket string) (*MinIOStorage, error) {
	return newMinIOClient(endpoint, accessKey, secretKey, bucket, false, false, "")
}

func NewExternalS3Storage(endpoint, accessKey, secretKey, bucket string) (*MinIOStorage, error) {
	return newMinIOClient(endpoint, accessKey, secretKey, bucket, true, false, "")
}

func newMinIOClient(rawEndpoint, accessKey, secretKey, bucket string, secure, insecureTLS bool, proxyAddress string) (*MinIOStorage, error) {
	endpoint := strings.TrimSpace(rawEndpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("minio endpoint is required")
	}
	if strings.Contains(endpoint, "://") || strings.ContainsAny(endpoint, "/?#") {
		return nil, fmt.Errorf("invalid minio endpoint")
	}

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	}
	if insecureTLS && secure {
		transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		if proxyAddress != "" {
			dialer := &net.Dialer{}
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp", proxyAddress)
			}
		}
		opts.Transport = transport
	} else if proxyAddress != "" {
		transport := &http.Transport{}
		dialer := &net.Dialer{}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", proxyAddress)
		}
		opts.Transport = transport
	}

	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("minio New: %w", err)
	}

	return &MinIOStorage{client: client, publicClient: client, accessKey: accessKey, secretKey: secretKey, bucket: bucket}, nil
}

// SetPublicEndpoint keeps server-side operations on the private endpoint while
// making browser-facing presigned URLs use the HTTPS Envoy endpoint.
func (m *MinIOStorage) SetPublicEndpoint(endpoint string) error {
	return m.SetPublicEndpointViaProxy(endpoint, false, "")
}

func (m *MinIOStorage) SetPublicEndpointInsecure(endpoint string, insecureTLS bool) error {
	return m.SetPublicEndpointViaProxy(endpoint, insecureTLS, "")
}

func (m *MinIOStorage) SetPublicEndpointViaProxy(endpoint string, insecureTLS bool, proxyAddress string) error {
	public, err := newMinIOClient(endpoint, m.accessKey, m.secretKey, m.bucket, true, insecureTLS, proxyAddress)
	if err != nil {
		return err
	}
	m.publicClient = public.client
	return nil
}

func (m *MinIOStorage) PutObject(ctx context.Context, key string, r io.Reader, size int64, opts PutOptions) (ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return ObjectInfo{}, err
	}

	if r == nil {
		return ObjectInfo{}, fmt.Errorf("put object %q: reader is nil", key)
	}

	if size < -1 {
		return ObjectInfo{}, fmt.Errorf("put object %q: invalid size %d", key, size)
	}

	putOpts := minio.PutObjectOptions{}
	if opts.ContentType != "" {
		putOpts.ContentType = opts.ContentType
	}

	// minio requires io.Reader and size; if size unknown use -1
	_, err := m.client.PutObject(ctx, m.bucket, key, r, size, putOpts)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("put object %q: %w", key, err)
	}

	// return basic info
	info, err := m.StatObject(ctx, key)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat object after put %q: %w", key, err)
	}

	return info, nil
}

func (m *MinIOStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return nil, ObjectInfo{}, err
	}

	obj, err := m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("minio GetObject: %w", err)
	}

	info, err := m.StatObject(ctx, key)
	if err != nil {
		_ = obj.Close()
		return nil, ObjectInfo{}, fmt.Errorf("stat object after get %q: %w", key, err)
	}

	return obj, info, nil
}

func (m *MinIOStorage) DeleteObject(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	if err := m.client.RemoveObject(ctx, m.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete object %q: %w", key, err)
	}

	return nil
}

func (m *MinIOStorage) ListObjects(ctx context.Context, prefix string, opts ListOptions) (ListResult, error) {
	if prefix != "" {
		if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
			return ListResult{}, fmt.Errorf("invalid list prefix: %w", err)
		}
	}

	ch := m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})

	var res ListResult
	for o := range ch {
		if o.Err != nil {
			return ListResult{}, fmt.Errorf("list objects with prefix %q: %w", prefix, o.Err)
		}
		if opts.Limit > 0 && len(res.Objects) >= opts.Limit {
			continue
		}
		res.Objects = append(res.Objects, ObjectInfo{
			Key:          o.Key,
			Size:         o.Size,
			LastModified: o.LastModified,
			ETag:         o.ETag,
		})
	}

	return res, nil
}

func (m *MinIOStorage) StatObject(ctx context.Context, key string) (ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return ObjectInfo{}, err
	}

	si, err := m.client.StatObject(ctx, m.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("minio StatObject: %w", err)
	}

	return ObjectInfo{
		Key:          si.Key,
		Size:         si.Size,
		LastModified: si.LastModified,
		ETag:         si.ETag,
	}, nil
}

func (m *MinIOStorage) PresignedGetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}

	if expiry <= 0 {
		return "", fmt.Errorf("presign object %q: expiry must be positive", key)
	}

	client := m.publicClient
	if client == nil {
		client = m.client
	}
	u, err := client.PresignedGetObject(ctx, m.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("minio PresignedGetObject: %w", err)
	}

	return u.String(), nil
}

func (m *MinIOStorage) PresignedPutURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}

	if expiry <= 0 || expiry > time.Hour {
		return "", fmt.Errorf("presign upload expiry must be between 0 and 1 hour")
	}

	client := m.publicClient
	if client == nil {
		client = m.client
	}
	u, err := client.PresignedPutObject(ctx, m.bucket, key, expiry)
	if err != nil {
		return "", fmt.Errorf("minio PresignedPutObject: %w", err)
	}

	return u.String(), nil
}

func validateKey(key string) error {
	if strings.TrimSpace(key) == "" || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00\r\n") {
		return fmt.Errorf("object key must not be empty")
	}

	for _, part := range strings.Split(key, "/") {
		if part == ".." || part == "." {
			return fmt.Errorf("object key contains an unsafe path segment")
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("object key contains control characters")
			}
		}
	}

	return nil
}
