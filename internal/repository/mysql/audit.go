package mysql

import (
	"context"
	"encoding/json"
	"fmt"
)

// WriteAuditLog stores an audit event. An empty details value is stored as NULL.
func (i *Implementation) WriteAuditLog(ctx context.Context, userID uint64, action string, details json.RawMessage) error {
	var payload any
	if len(details) > 0 {
		payload = string(details)
	}
	if _, err := i.Conn().ExecContext(ctx, `
		INSERT INTO audit_log (user_id, action, details) VALUES (?, ?, ?)`,
		userID, action, payload); err != nil {
		return fmt.Errorf("WriteAuditLog: %w", err)
	}
	return nil
}
