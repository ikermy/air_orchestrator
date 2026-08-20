package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type contextUserIDKey struct{}

func userIDFromContext(ctx context.Context) (uint32, error) {
	userID, ok := ctx.Value(contextUserIDKey{}).(uint32)
	if !ok || userID == 0 {
		return 0, status.Error(codes.Unauthenticated, "user identity is missing")
	}
	return userID, nil
}

// WithUserID is used by the authentication interceptor and proxy tests.
func WithUserID(ctx context.Context, userID uint32) context.Context {
	return context.WithValue(ctx, contextUserIDKey{}, userID)
}
