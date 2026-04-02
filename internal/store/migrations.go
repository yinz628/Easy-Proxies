package store

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Migration represents a single database schema migration.
type Migration struct {
	Version     int
	Description string
	Up          string
}

// allMigrations returns all migrations in order. New migrations should be
// appended to this list with incrementing version numbers.
func allMigrations() []Migration {
	return []Migration{
		{
			Version:     1,
			Description: "initial schema",
			Up: `
-- Nodes table: stores all proxy nodes
CREATE TABLE IF NOT EXISTS nodes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    uri        TEXT    NOT NULL,
    name       TEXT    NOT NULL DEFAULT '',
    source     TEXT    NOT NULL DEFAULT 'manual',
    port       INTEGER NOT NULL DEFAULT 0,
    username   TEXT    NOT NULL DEFAULT '',
    password   TEXT    NOT NULL DEFAULT '',
    region     TEXT    NOT NULL DEFAULT '',
    country    TEXT    NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(uri)
);

CREATE INDEX IF NOT EXISTS idx_nodes_source  ON nodes(source);
CREATE INDEX IF NOT EXISTS idx_nodes_region  ON nodes(region);
CREATE INDEX IF NOT EXISTS idx_nodes_enabled ON nodes(enabled);

-- Node runtime statistics
CREATE TABLE IF NOT EXISTS node_stats (
    node_id           INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    failure_count     INTEGER NOT NULL DEFAULT 0,
    success_count     INTEGER NOT NULL DEFAULT 0,
    blacklisted       INTEGER NOT NULL DEFAULT 0,
    blacklisted_until TEXT    NOT NULL DEFAULT '',
    last_error        TEXT    NOT NULL DEFAULT '',
    last_failure_at   TEXT    NOT NULL DEFAULT '',
    last_success_at   TEXT    NOT NULL DEFAULT '',
    last_latency_ms   INTEGER NOT NULL DEFAULT -1,
    available         INTEGER NOT NULL DEFAULT 0,
    initial_check_done INTEGER NOT NULL DEFAULT 0,
    updated_at        TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Node event timeline for debug tracking
CREATE TABLE IF NOT EXISTS node_timeline (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    success    INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    error      TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_timeline_node_id ON node_timeline(node_id);

-- User authentication sessions
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- Subscription refresh status (singleton row)
CREATE TABLE IF NOT EXISTS subscription_status (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    last_refresh   TEXT    NOT NULL DEFAULT '',
    next_refresh   TEXT    NOT NULL DEFAULT '',
    node_count     INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT    NOT NULL DEFAULT '',
    refresh_count  INTEGER NOT NULL DEFAULT 0,
    is_refreshing  INTEGER NOT NULL DEFAULT 0,
    nodes_hash     TEXT    NOT NULL DEFAULT '',
    updated_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Insert the singleton subscription status row
INSERT OR IGNORE INTO subscription_status (id) VALUES (1);
`,
		},
		{
			Version:     2,
			Description: "add traffic columns to node_stats",
			Up: `
ALTER TABLE node_stats ADD COLUMN total_upload_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE node_stats ADD COLUMN total_download_bytes INTEGER NOT NULL DEFAULT 0;
`,
		},
		{
			Version:     3,
			Description: "add feed key to nodes",
			Up: `
ALTER TABLE nodes ADD COLUMN feed_key TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_nodes_feed_key ON nodes(feed_key);
`,
		},
		{
			Version:     4,
			Description: "add txt feed memberships",
			Up: `
CREATE TABLE IF NOT EXISTS txt_feed_memberships (
    feed_key   TEXT NOT NULL,
    uri        TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (feed_key, uri)
);

CREATE INDEX IF NOT EXISTS idx_txt_feed_memberships_uri ON txt_feed_memberships(uri);
`,
		},
		{
			Version:     5,
			Description: "add proxy quality summary and details",
			Up: `
ALTER TABLE node_stats ADD COLUMN quality_status TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN quality_score INTEGER;
ALTER TABLE node_stats ADD COLUMN quality_grade TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN quality_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN quality_checked_at TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN exit_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN exit_country TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN exit_country_code TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN exit_region TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS node_quality_checks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target     TEXT    NOT NULL,
    status     TEXT    NOT NULL,
    http_status INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    message    TEXT    NOT NULL DEFAULT '',
    checked_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_node_quality_checks_node_id ON node_quality_checks(node_id);
`,
		},
		{
			Version:     6,
			Description: "add quality version and provider statuses",
			Up: `
ALTER TABLE node_stats ADD COLUMN quality_version TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN quality_openai_status TEXT NOT NULL DEFAULT '';
ALTER TABLE node_stats ADD COLUMN quality_anthropic_status TEXT NOT NULL DEFAULT '';
`,
		},
	}
}

// Migrate applies all pending migrations to the database.
// Each migration runs in its own transaction. Already-applied migrations
// (tracked in schema_migrations) are skipped.
func Migrate(db *sql.DB) error {
	// Create the migrations tracking table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     INTEGER PRIMARY KEY,
			applied_at  TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	// Get the current version
	var currentVersion int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations")
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("query current version: %w", err)
	}

	migrations := allMigrations()
	applied := 0

	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		log.Printf("[store] applying migration %d: %s", m.Version, m.Description)

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.Version, err)
		}

		if _, err := tx.Exec(m.Up); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %d (%s): %w", m.Version, m.Description, err)
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at, description) VALUES (?, ?, ?)",
			m.Version, time.Now().UTC().Format(time.RFC3339), m.Description,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.Version, err)
		}

		applied++
		log.Printf("[store] migration %d applied successfully", m.Version)
	}

	if applied > 0 {
		log.Printf("[store] %d migration(s) applied, current version: %d", applied, migrations[len(migrations)-1].Version)
	} else {
		log.Printf("[store] database schema is up to date (version %d)", currentVersion)
	}

	return nil
}

// CurrentVersion returns the current schema version.
func CurrentVersion(db *sql.DB) (int, error) {
	// Check if schema_migrations table exists
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type='table' AND name='schema_migrations'
	`).Scan(&count)
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}

	var version int
	err = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	return version, err
}
