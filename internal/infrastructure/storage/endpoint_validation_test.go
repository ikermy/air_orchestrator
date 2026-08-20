package storage

import "testing"

func TestValidateExternalEndpointRejectsUnsafeEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:9000", "http://169.254.169.254/latest", "http://localhost:9000",
		"ftp://s3.example.com", "https://s3.example.com/path", "https://user:pass@s3.example.com",
	} {
		if err := ValidateExternalEndpoint(t.Context(), endpoint, true); err == nil {
			t.Errorf("ValidateExternalEndpoint(%q) accepted unsafe endpoint", endpoint)
		}
	}
}

func TestValidateExternalEndpointRejectsHTTPInProduction(t *testing.T) {
	if err := ValidateExternalEndpoint(t.Context(), "http://s3.example.com", true); err == nil {
		t.Fatal("HTTP endpoint accepted in production")
	}
}

func TestValidateBucketName(t *testing.T) {
	for _, bucket := range []string{"ab", "BadBucket", "bucket..name", ".bucket", "bucket.", "bucket_name"} {
		if err := ValidateBucketName(bucket); err == nil {
			t.Errorf("bucket %q was accepted", bucket)
		}
	}
	for _, bucket := range []string{"user-files", "s3.example.com", "bucket123"} {
		if err := ValidateBucketName(bucket); err != nil {
			t.Errorf("bucket %q rejected: %v", bucket, err)
		}
	}
}
