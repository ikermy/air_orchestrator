//go:build integration

package mysql

import (
	"context"
	"os"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/migrate"
)

// TestApplyMigrationsLocal applies all embedded migrations when RUN_MIGRATIONS=true.
func TestApplyMigrationsLocal(t *testing.T) {
	if os.Getenv("RUN_MIGRATIONS") != "true" {
		t.Skip("set RUN_MIGRATIONS=true to apply migrations")
	}

	db, err := comdb.New(context.Background())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	runner := migrate.New(db.Conn())
	if err := runner.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	applied, err := runner.Up(context.Background())
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	version, dirty, err := runner.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	t.Logf("applied=%d version=%d dirty=%v", applied, version, dirty)
}
