package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations from the embedded migrations directory.
func Migrate(ctx context.Context, db *sql.DB) error {
	// Ensure schema_migrations table exists
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations dir: %w", err)
	}

	type migrationFile struct {
		version int
		name    string
	}

	var files []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		files = append(files, migrationFile{version: ver, name: entry.Name()})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].version < files[j].version
	})

	for _, m := range files {
		var exists bool
		err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", m.version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration version %d: %w", m.version, err)
		}
		if exists {
			continue
		}

		content, err := migrationsFS.ReadFile(filepath.ToSlash(filepath.Join("migrations", m.name)))
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", m.name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin tx for migration %d: %w", m.version, err)
		}

		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %w", m.name, err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", m.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", m.version, err)
		}
	}

	return nil
}
