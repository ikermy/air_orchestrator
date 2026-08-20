package storage

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7/pkg/credentials"
)

type contextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req.WithContext(t.ctx))
}

// ProbeSTSCapability performs a short-lived AssumeRole request. It never
// returns or persists the resulting credentials; callers only persist the
// capability result and still keep the configured credentials encrypted.
func ProbeSTSCapability(ctx context.Context, endpoint, accessKey, secretKey, bucket, region string) (bool, error) {
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return false, fmt.Errorf("incomplete S3 STS probe configuration")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	httpClient := &http.Client{
		Timeout: 8 * time.Second,
		Transport: contextTransport{
			ctx:  probeCtx,
			base: http.DefaultTransport,
		},
	}
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::%s"]}]}`, bucket)
	provider, err := credentials.NewSTSAssumeRole(endpoint, credentials.STSAssumeRoleOptions{
		AccessKey: accessKey, SecretKey: secretKey, Policy: policy,
		DurationSeconds: 900, RoleARN: "assumeRole", RoleSessionName: "airorc-sts-probe", Location: region,
	})
	if err != nil {
		return false, err
	}
	if _, err = provider.GetWithContext(&credentials.CredContext{Client: httpClient}); err != nil {
		return false, err
	}
	return true, nil
}
