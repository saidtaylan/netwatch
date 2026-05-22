package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// applyMigrations runs all .sql files in migrations/ in lexicographic
// order. A meta table (schema_migrations) tracks which files have already
// been applied so subsequent boots skip them.
//
// Migration files are versioned by their filename prefix (001_, 002_, …).
// Within a single file, statements are split by ";\n" and each is executed
// in the same transaction — partial application is impossible.
func applyMigrations(db *sql.DB) error {
	// Ensure tracking table exists before we look at anything else.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Deterministic order: lex by filename.
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		var alreadyApplied int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, name,
		).Scan(&alreadyApplied)
		if err != nil {
			return fmt.Errorf("check %s: %w", name, err)
		}
		if alreadyApplied > 0 {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, datetime('now'))`,
			name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}
	return nil
}
