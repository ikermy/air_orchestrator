package exam

import (
	"testing"
)

// Snowflake IDs exceed 2^53, so MapClaims (float64) would lose precision.
// The typed claims parser must round-trip them exactly.
func TestAccessTokenRoundTripSnowflakeUserID(t *testing.T) {
	e := &Exam{sessionKey: []byte("test-session-key")}
	userID := uint64(1)<<53 + 123456789
	respID := uint64(1)<<60 + 7

	sta, _, err := e.GenerateAccessToken(userID, respID)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}

	gotUser, gotResp, err := e.VerifyAccessToken(sta)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if gotUser != userID {
		t.Fatalf("VerifyAccessToken userID = %d, want %d", gotUser, userID)
	}
	if gotResp != respID {
		t.Fatalf("VerifyAccessToken respID = %d, want %d", gotResp, respID)
	}

	ptrUser, ptrResp, err := e.ParseAuthToken(&sta)
	if err != nil {
		t.Fatalf("ParseAuthToken: %v", err)
	}
	if ptrUser == nil || *ptrUser != userID || ptrResp == nil || *ptrResp != respID {
		t.Fatalf("ParseAuthToken = (%v, %v), want (%d, %d)", ptrUser, ptrResp, userID, respID)
	}
}

func TestMailTokenRoundTripSnowflakeUserID(t *testing.T) {
	e := &Exam{sessionKey: []byte("test-session-key")}
	userID := uint64(1)<<53 + 42

	token, err := e.GetMailConfirmationToken(userID, "test@example.com")
	if err != nil {
		t.Fatalf("GetMailConfirmationToken: %v", err)
	}

	gotUser, email, err := e.ParseMailConfirmationToken(token)
	if err != nil {
		t.Fatalf("ParseMailConfirmationToken: %v", err)
	}
	if gotUser != userID || email != "test@example.com" {
		t.Fatalf("ParseMailConfirmationToken = (%d, %q), want (%d, %q)", gotUser, email, userID, "test@example.com")
	}
}
