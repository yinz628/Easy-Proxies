package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore implements Store using SQLite.
type sqliteStore struct {
	db *sql.DB
	tx *sql.Tx // non-nil when operating inside WithTx
}

// Open creates a new SQLite-backed Store at the given path.
// It applies all pending migrations and sets optimal PRAGMAs.
func Open(dbPath string) (Store, error) {
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)&_pragma=foreign_keys(ON)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}

	// Connection pool settings
	db.SetMaxOpenConns(1) // SQLite only supports 1 writer
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0) // connections don't expire

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Run migrations
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Printf("[store] SQLite store opened: %s", dbPath)
	return &sqliteStore{db: db}, nil
}

// conn returns the underlying *sql.Tx or *sql.DB for executing queries.
func (s *sqliteStore) conn() querier {
	if s.tx != nil {
		return s.tx
	}
	return s.db
}

// querier abstracts *sql.DB and *sql.Tx for query execution.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// ===================== Node operations =====================

func (s *sqliteStore) ListNodes(ctx context.Context, filter NodeFilter) ([]Node, error) {
	query := "SELECT id, uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at FROM nodes"
	var conditions []string
	var args []any

	if filter.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.Region != "" {
		conditions = append(conditions, "region = ?")
		args = append(args, filter.Region)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = ?")
		if *filter.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if lifecycleState := strings.TrimSpace(filter.LifecycleState); lifecycleState != "" {
		conditions = append(conditions, "lifecycle_state = ?")
		args = append(args, lifecycleState)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
		if filter.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", filter.Offset)
		}
	}

	rows, err := s.conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

func (s *sqliteStore) GetNode(ctx context.Context, id int64) (*Node, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT id, uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at FROM nodes WHERE id = ?", id)
	return scanNode(row)
}

func (s *sqliteStore) GetNodeByURI(ctx context.Context, uri string) (*Node, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT id, uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at FROM nodes WHERE uri = ?", uri)
	return scanNode(row)
}

func (s *sqliteStore) GetNodeByName(ctx context.Context, name string) (*Node, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT id, uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at FROM nodes WHERE name = ?", name)
	return scanNode(row)
}

func (s *sqliteStore) CreateNode(ctx context.Context, node *Node) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now().UTC()
	}
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = time.Now().UTC()
	}
	lifecycleState := normalizeNodeLifecycle(node.LifecycleState, node.Enabled)
	node.LifecycleState = lifecycleState
	node.Enabled = lifecycleStateEnabled(lifecycleState)
	enabled := boolToInt(node.Enabled)

	result, err := s.conn().ExecContext(ctx,
		`INSERT INTO nodes (uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.URI, node.Name, node.Source, node.FeedKey, node.Port,
		node.Username, node.Password, node.Region, node.Country,
		enabled, lifecycleState, now, now,
	)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	node.ID = id

	// Create initial stats row
	_, err = s.conn().ExecContext(ctx,
		"INSERT OR IGNORE INTO node_stats (node_id) VALUES (?)", id)
	if err != nil {
		return fmt.Errorf("create initial node stats: %w", err)
	}

	return nil
}

func (s *sqliteStore) UpdateNode(ctx context.Context, node *Node) error {
	now := time.Now().UTC().Format(time.RFC3339)
	lifecycleState := normalizeNodeLifecycle(node.LifecycleState, node.Enabled)
	node.LifecycleState = lifecycleState
	node.Enabled = lifecycleStateEnabled(lifecycleState)
	enabled := boolToInt(node.Enabled)

	result, err := s.conn().ExecContext(ctx,
		`UPDATE nodes SET uri=?, name=?, source=?, feed_key=?, port=?, username=?, password=?,
		 region=?, country=?, enabled=?, lifecycle_state=?, updated_at=?
		 WHERE id=?`,
		node.URI, node.Name, node.Source, node.FeedKey, node.Port,
		node.Username, node.Password, node.Region, node.Country,
		enabled, lifecycleState, now, node.ID,
	)
	if err != nil {
		return fmt.Errorf("update node %d: %w", node.ID, err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("node %d not found", node.ID)
	}
	return nil
}

func (s *sqliteStore) DeleteNode(ctx context.Context, id int64) error {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete node %d: %w", id, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("node %d not found", id)
	}
	return nil
}

func (s *sqliteStore) DeleteNodesBySource(ctx context.Context, source string) (int64, error) {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM nodes WHERE source = ?", source)
	if err != nil {
		return 0, fmt.Errorf("delete nodes by source %q: %w", source, err)
	}
	return result.RowsAffected()
}

func (s *sqliteStore) DeleteNodesByFeedKey(ctx context.Context, feedKey string) (int64, error) {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM nodes WHERE feed_key = ?", feedKey)
	if err != nil {
		return 0, fmt.Errorf("delete nodes by feed key %q: %w", feedKey, err)
	}
	return result.RowsAffected()
}

func (s *sqliteStore) ReplaceTXTFeedNodes(ctx context.Context, feedKey string, nodes []Node) error {
	feedKey = strings.TrimSpace(feedKey)
	if feedKey == "" {
		return fmt.Errorf("replace txt feed nodes: empty feed key")
	}

	execFn := func(txStore *sqliteStore) error {
		now := time.Now().UTC().Format(time.RFC3339)

		if _, err := txStore.conn().ExecContext(ctx, "DELETE FROM txt_feed_memberships WHERE feed_key = ?", feedKey); err != nil {
			return fmt.Errorf("delete old txt feed memberships %q: %w", feedKey, err)
		}

		for i := range nodes {
			n := &nodes[i]
			n.Source = NodeSourceTXTSubscription
			n.FeedKey = feedKey
			lifecycleState := normalizeNodeLifecycle(n.LifecycleState, n.Enabled)
			n.LifecycleState = lifecycleState
			n.Enabled = lifecycleStateEnabled(lifecycleState)
			enabled := boolToInt(n.Enabled)

			if _, err := txStore.conn().ExecContext(ctx,
				`INSERT OR IGNORE INTO nodes (uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				n.URI, n.Name, n.Source, feedKey, n.Port, n.Username, n.Password, n.Region, n.Country, enabled, lifecycleState, now, now,
			); err != nil {
				return fmt.Errorf("insert txt node %q: %w", n.URI, err)
			}

			if _, err := txStore.conn().ExecContext(ctx,
				`UPDATE nodes
				   SET name = CASE WHEN source = ? THEN ? ELSE name END,
				       port = CASE WHEN source = ? THEN ? ELSE port END,
				       username = CASE WHEN source = ? THEN ? ELSE username END,
				       password = CASE WHEN source = ? THEN ? ELSE password END,
				       region = CASE WHEN source = ? THEN ? ELSE region END,
				       country = CASE WHEN source = ? THEN ? ELSE country END,
				       enabled = CASE WHEN source = ? THEN ? ELSE enabled END,
				       lifecycle_state = CASE WHEN source = ? THEN ? ELSE lifecycle_state END,
				       updated_at = ?
				 WHERE uri = ?`,
				NodeSourceTXTSubscription, n.Name,
				NodeSourceTXTSubscription, n.Port,
				NodeSourceTXTSubscription, n.Username,
				NodeSourceTXTSubscription, n.Password,
				NodeSourceTXTSubscription, n.Region,
				NodeSourceTXTSubscription, n.Country,
				NodeSourceTXTSubscription, enabled,
				NodeSourceTXTSubscription, lifecycleState,
				now, n.URI,
			); err != nil {
				return fmt.Errorf("update txt node %q: %w", n.URI, err)
			}

			if _, err := txStore.conn().ExecContext(ctx,
				"INSERT OR IGNORE INTO node_stats (node_id) SELECT id FROM nodes WHERE uri = ?",
				n.URI,
			); err != nil {
				return fmt.Errorf("create txt node stats %q: %w", n.URI, err)
			}

			if _, err := txStore.conn().ExecContext(ctx,
				`INSERT OR REPLACE INTO txt_feed_memberships (feed_key, uri, created_at)
				 VALUES (?, ?, ?)`,
				feedKey, n.URI, now,
			); err != nil {
				return fmt.Errorf("insert txt feed membership %q/%q: %w", feedKey, n.URI, err)
			}
		}

		if _, err := txStore.conn().ExecContext(ctx,
			`DELETE FROM nodes
			  WHERE source = ?
			    AND uri NOT IN (SELECT uri FROM txt_feed_memberships)`,
			NodeSourceTXTSubscription,
		); err != nil {
			return fmt.Errorf("delete orphan txt nodes: %w", err)
		}

		if _, err := txStore.conn().ExecContext(ctx,
			`UPDATE nodes
			    SET feed_key = COALESCE(
			        (SELECT MIN(m.feed_key) FROM txt_feed_memberships m WHERE m.uri = nodes.uri),
			        ''
			    )
			  WHERE source = ?`,
			NodeSourceTXTSubscription,
		); err != nil {
			return fmt.Errorf("refresh txt node feed keys: %w", err)
		}

		return nil
	}

	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) BulkUpsertNodes(ctx context.Context, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}

	execFn := func(txStore *sqliteStore) error {
		now := time.Now().UTC().Format(time.RFC3339)
		stmt, err := txStore.conn().PrepareContext(ctx,
			`INSERT INTO nodes (uri, name, source, feed_key, port, username, password, region, country, enabled, lifecycle_state, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(uri) DO UPDATE SET
			   name=excluded.name, source=excluded.source, feed_key=excluded.feed_key, port=excluded.port,
			   username=excluded.username, password=excluded.password,
			   region=excluded.region, country=excluded.country,
			   enabled=excluded.enabled, lifecycle_state=excluded.lifecycle_state,
			   updated_at=excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("prepare bulk upsert: %w", err)
		}
		defer stmt.Close()

		for i := range nodes {
			n := &nodes[i]
			lifecycleState := normalizeNodeLifecycle(n.LifecycleState, n.Enabled)
			n.LifecycleState = lifecycleState
			n.Enabled = lifecycleStateEnabled(lifecycleState)
			enabled := boolToInt(n.Enabled)
			result, err := stmt.ExecContext(ctx,
				n.URI, n.Name, n.Source, n.FeedKey, n.Port,
				n.Username, n.Password, n.Region, n.Country,
				enabled, lifecycleState, now, now,
			)
			if err != nil {
				return fmt.Errorf("upsert node %q: %w", n.URI, err)
			}
			id, _ := result.LastInsertId()
			if id > 0 {
				n.ID = id
			}
		}

		// Create stats rows for new nodes
		_, err = txStore.conn().ExecContext(ctx,
			"INSERT OR IGNORE INTO node_stats (node_id) SELECT id FROM nodes")
		if err != nil {
			return fmt.Errorf("create stats for new nodes: %w", err)
		}

		return nil
	}

	// If already in a transaction, execute directly
	if s.tx != nil {
		return execFn(s)
	}

	// Otherwise wrap in a transaction
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) CountNodes(ctx context.Context, filter NodeFilter) (int64, error) {
	query := "SELECT COUNT(*) FROM nodes"
	var conditions []string
	var args []any

	if filter.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.Region != "" {
		conditions = append(conditions, "region = ?")
		args = append(args, filter.Region)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = ?")
		if *filter.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if lifecycleState := strings.TrimSpace(filter.LifecycleState); lifecycleState != "" {
		conditions = append(conditions, "lifecycle_state = ?")
		args = append(args, lifecycleState)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var count int64
	err := s.conn().QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

// ===================== Node stats =====================

func (s *sqliteStore) GetNodeStats(ctx context.Context, nodeID int64) (*NodeStats, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT node_id, failure_count, success_count, blacklisted, blacklisted_until,
		 last_error, last_failure_at, last_success_at, last_latency_ms,
		 available, initial_check_done, total_upload_bytes, total_download_bytes,
		 quality_status, quality_version, quality_openai_status, quality_anthropic_status,
		 quality_score, quality_grade, quality_summary, quality_checked_at,
		 exit_ip, exit_country, exit_country_code, exit_region, updated_at
		 FROM node_stats WHERE node_id = ?`, nodeID)

	stats := &NodeStats{}
	var blacklistedUntilStr, lastFailureStr, lastSuccessStr, qualityCheckedAtStr, updatedAtStr string
	var qualityScore sql.NullInt64
	var blacklisted, available, initialCheckDone int

	err := row.Scan(
		&stats.NodeID, &stats.FailureCount, &stats.SuccessCount,
		&blacklisted, &blacklistedUntilStr,
		&stats.LastError, &lastFailureStr, &lastSuccessStr,
		&stats.LastLatencyMs, &available, &initialCheckDone,
		&stats.TotalUploadBytes, &stats.TotalDownloadBytes,
		&stats.QualityStatus, &stats.QualityVersion, &stats.QualityOpenAIStatus, &stats.QualityAnthropicStatus,
		&qualityScore, &stats.QualityGrade, &stats.QualitySummary, &qualityCheckedAtStr,
		&stats.ExitIP, &stats.ExitCountry, &stats.ExitCountryCode, &stats.ExitRegion, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get node stats %d: %w", nodeID, err)
	}

	stats.Blacklisted = blacklisted != 0
	stats.Available = available != 0
	stats.InitialCheckDone = initialCheckDone != 0
	stats.BlacklistedUntil = parseTime(blacklistedUntilStr)
	stats.LastFailureAt = parseTime(lastFailureStr)
	stats.LastSuccessAt = parseTime(lastSuccessStr)
	if qualityScore.Valid {
		value := int(qualityScore.Int64)
		stats.QualityScore = &value
	}
	stats.QualityCheckedAt = parseTime(qualityCheckedAtStr)
	stats.UpdatedAt = parseTime(updatedAtStr)

	return stats, nil
}

func (s *sqliteStore) UpsertNodeStats(ctx context.Context, stats *NodeStats) error {
	now := time.Now().UTC().Format(time.RFC3339)
	blacklisted := 0
	if stats.Blacklisted {
		blacklisted = 1
	}
	available := 0
	if stats.Available {
		available = 1
	}
	initialCheckDone := 0
	if stats.InitialCheckDone {
		initialCheckDone = 1
	}
	var qualityScore any
	if stats.QualityScore != nil {
		qualityScore = *stats.QualityScore
	}

	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO node_stats (node_id, failure_count, success_count, blacklisted, blacklisted_until,
		 last_error, last_failure_at, last_success_at, last_latency_ms, available, initial_check_done,
		 total_upload_bytes, total_download_bytes, quality_status, quality_version, quality_openai_status,
		 quality_anthropic_status, quality_score, quality_grade, quality_summary,
		 quality_checked_at, exit_ip, exit_country, exit_country_code, exit_region, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
		   failure_count=excluded.failure_count, success_count=excluded.success_count,
		   blacklisted=excluded.blacklisted, blacklisted_until=excluded.blacklisted_until,
		   last_error=excluded.last_error, last_failure_at=excluded.last_failure_at,
		   last_success_at=excluded.last_success_at, last_latency_ms=excluded.last_latency_ms,
		   available=excluded.available, initial_check_done=excluded.initial_check_done,
		   total_upload_bytes=excluded.total_upload_bytes, total_download_bytes=excluded.total_download_bytes,
		   quality_status=excluded.quality_status, quality_version=excluded.quality_version,
		   quality_openai_status=excluded.quality_openai_status,
		   quality_anthropic_status=excluded.quality_anthropic_status,
		   quality_score=excluded.quality_score,
		   quality_grade=excluded.quality_grade, quality_summary=excluded.quality_summary,
		   quality_checked_at=excluded.quality_checked_at, exit_ip=excluded.exit_ip,
		   exit_country=excluded.exit_country, exit_country_code=excluded.exit_country_code,
		   exit_region=excluded.exit_region,
		   updated_at=excluded.updated_at`,
		stats.NodeID, stats.FailureCount, stats.SuccessCount,
		blacklisted, formatTime(stats.BlacklistedUntil),
		stats.LastError, formatTime(stats.LastFailureAt), formatTime(stats.LastSuccessAt),
		stats.LastLatencyMs, available, initialCheckDone,
		stats.TotalUploadBytes, stats.TotalDownloadBytes,
		stats.QualityStatus, stats.QualityVersion, stats.QualityOpenAIStatus,
		stats.QualityAnthropicStatus, qualityScore, stats.QualityGrade, stats.QualitySummary,
		formatTime(stats.QualityCheckedAt), stats.ExitIP, stats.ExitCountry, stats.ExitCountryCode, stats.ExitRegion,
		now,
	)
	return err
}

func (s *sqliteStore) RecordSuccess(ctx context.Context, nodeID int64, latencyMs int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 success_count = success_count + 1,
		 last_success_at = ?,
		 last_latency_ms = ?,
		 available = 1,
		 initial_check_done = 1,
		 updated_at = ?
		 WHERE node_id = ?`,
		now, latencyMs, now, nodeID,
	)
	return err
}

func (s *sqliteStore) RecordFailure(ctx context.Context, nodeID int64, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 failure_count = failure_count + 1,
		 last_error = ?,
		 last_failure_at = ?,
		 updated_at = ?
		 WHERE node_id = ?`,
		errMsg, now, now, nodeID,
	)
	return err
}

func (s *sqliteStore) SetBlacklist(ctx context.Context, nodeID int64, until time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 blacklisted = 1,
		 blacklisted_until = ?,
		 failure_count = 0,
		 updated_at = ?
		 WHERE node_id = ?`,
		formatTime(until), now, nodeID,
	)
	return err
}

func (s *sqliteStore) ClearBlacklist(ctx context.Context, nodeID int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 blacklisted = 0,
		 blacklisted_until = '',
		 updated_at = ?
		 WHERE node_id = ?`,
		now, nodeID,
	)
	return err
}

func (s *sqliteStore) ClearAllBlacklists(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET blacklisted = 0, blacklisted_until = '', updated_at = ? WHERE blacklisted = 1`,
		now,
	)
	return err
}

func (s *sqliteStore) BatchUpdateStats(ctx context.Context, updates []StatsUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	execFn := func(txStore *sqliteStore) error {
		now := time.Now().UTC().Format(time.RFC3339)
		stmt, err := txStore.conn().PrepareContext(ctx,
			`INSERT INTO node_stats (node_id, failure_count, success_count, blacklisted, blacklisted_until,
			 last_error, last_failure_at, last_success_at, last_latency_ms, available, initial_check_done,
			 total_upload_bytes, total_download_bytes, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(node_id) DO UPDATE SET
			   failure_count=excluded.failure_count, success_count=excluded.success_count,
			   blacklisted=excluded.blacklisted, blacklisted_until=excluded.blacklisted_until,
			   last_error=excluded.last_error, last_failure_at=excluded.last_failure_at,
			   last_success_at=excluded.last_success_at, last_latency_ms=excluded.last_latency_ms,
			   available=excluded.available, initial_check_done=excluded.initial_check_done,
			   total_upload_bytes=excluded.total_upload_bytes, total_download_bytes=excluded.total_download_bytes,
			   updated_at=excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("prepare batch stats: %w", err)
		}
		defer stmt.Close()

		for _, u := range updates {
			blacklisted := 0
			if u.Blacklisted {
				blacklisted = 1
			}
			available := 0
			if u.Available {
				available = 1
			}
			initialCheckDone := 0
			if u.InitialCheckDone {
				initialCheckDone = 1
			}

			_, err := stmt.ExecContext(ctx,
				u.NodeID, u.FailureCount, u.SuccessCount,
				blacklisted, formatTime(u.BlacklistedUntil),
				u.LastError, formatTime(u.LastFailureAt), formatTime(u.LastSuccessAt),
				u.LastLatencyMs, available, initialCheckDone,
				u.TotalUploadBytes, u.TotalDownloadBytes, now,
			)
			if err != nil {
				return fmt.Errorf("batch update stats for node %d: %w", u.NodeID, err)
			}
		}
		return nil
	}

	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) GetAllNodeStats(ctx context.Context) (map[int64]*NodeStats, error) {
	rows, err := s.conn().QueryContext(ctx,
		`SELECT node_id, failure_count, success_count, blacklisted, blacklisted_until,
		 last_error, last_failure_at, last_success_at, last_latency_ms,
		 available, initial_check_done, total_upload_bytes, total_download_bytes,
		 quality_status, quality_version, quality_openai_status, quality_anthropic_status,
		 quality_score, quality_grade, quality_summary, quality_checked_at,
		 exit_ip, exit_country, exit_country_code, exit_region, updated_at
		 FROM node_stats`)
	if err != nil {
		return nil, fmt.Errorf("get all node stats: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]*NodeStats)
	for rows.Next() {
		stats := &NodeStats{}
		var blacklistedUntilStr, lastFailureStr, lastSuccessStr, qualityCheckedAtStr, updatedAtStr string
		var qualityScore sql.NullInt64
		var blacklisted, available, initialCheckDone int

		err := rows.Scan(
			&stats.NodeID, &stats.FailureCount, &stats.SuccessCount,
			&blacklisted, &blacklistedUntilStr,
			&stats.LastError, &lastFailureStr, &lastSuccessStr,
			&stats.LastLatencyMs, &available, &initialCheckDone,
			&stats.TotalUploadBytes, &stats.TotalDownloadBytes,
			&stats.QualityStatus, &stats.QualityVersion, &stats.QualityOpenAIStatus, &stats.QualityAnthropicStatus,
			&qualityScore, &stats.QualityGrade, &stats.QualitySummary, &qualityCheckedAtStr,
			&stats.ExitIP, &stats.ExitCountry, &stats.ExitCountryCode, &stats.ExitRegion, &updatedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node stats: %w", err)
		}

		stats.Blacklisted = blacklisted != 0
		stats.Available = available != 0
		stats.InitialCheckDone = initialCheckDone != 0
		stats.BlacklistedUntil = parseTime(blacklistedUntilStr)
		stats.LastFailureAt = parseTime(lastFailureStr)
		stats.LastSuccessAt = parseTime(lastSuccessStr)
		if qualityScore.Valid {
			value := int(qualityScore.Int64)
			stats.QualityScore = &value
		}
		stats.QualityCheckedAt = parseTime(qualityCheckedAtStr)
		stats.UpdatedAt = parseTime(updatedAtStr)

		result[stats.NodeID] = stats
	}
	return result, rows.Err()
}

func (s *sqliteStore) GetNodeQualityCheck(ctx context.Context, nodeID int64) (*NodeQualityCheck, error) {
	stats, err := s.GetNodeStats(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		return nil, nil
	}

	rows, err := s.conn().QueryContext(ctx,
		`SELECT target, status, http_status, latency_ms, message
		   FROM node_quality_checks
		  WHERE node_id = ?
		  ORDER BY id ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("get node quality check items %d: %w", nodeID, err)
	}
	defer rows.Close()

	items := make([]NodeQualityCheckItem, 0)
	for rows.Next() {
		var item NodeQualityCheckItem
		if err := rows.Scan(&item.Target, &item.Status, &item.HTTPStatus, &item.LatencyMs, &item.Message); err != nil {
			return nil, fmt.Errorf("scan node quality check item: %w", err)
		}
		items = append(items, item)
	}

	return &NodeQualityCheck{
		NodeID:                 nodeID,
		QualityStatus:          stats.QualityStatus,
		QualityVersion:         stats.QualityVersion,
		QualityOpenAIStatus:    stats.QualityOpenAIStatus,
		QualityAnthropicStatus: stats.QualityAnthropicStatus,
		QualityScore:           stats.QualityScore,
		QualityGrade:           stats.QualityGrade,
		QualitySummary:         stats.QualitySummary,
		QualityCheckedAt:       stats.QualityCheckedAt,
		ExitIP:                 stats.ExitIP,
		ExitCountry:            stats.ExitCountry,
		ExitCountryCode:        stats.ExitCountryCode,
		ExitRegion:             stats.ExitRegion,
		Items:                  items,
	}, rows.Err()
}

func (s *sqliteStore) SaveNodeQualityCheck(ctx context.Context, check *NodeQualityCheck) error {
	if check == nil {
		return nil
	}

	execFn := func(txStore *sqliteStore) error {
		score := any(nil)
		if check.QualityScore != nil {
			score = *check.QualityScore
		}

		_, err := txStore.conn().ExecContext(ctx,
			`UPDATE node_stats
			    SET quality_status = ?,
			        quality_version = ?,
			        quality_openai_status = ?,
			        quality_anthropic_status = ?,
			        quality_score = ?,
			        quality_grade = ?,
			        quality_summary = ?,
			        quality_checked_at = ?,
			        exit_ip = ?,
			        exit_country = ?,
			        exit_country_code = ?,
			        exit_region = ?,
			        updated_at = ?
			  WHERE node_id = ?`,
			check.QualityStatus,
			check.QualityVersion,
			check.QualityOpenAIStatus,
			check.QualityAnthropicStatus,
			score,
			check.QualityGrade,
			check.QualitySummary,
			formatTime(check.QualityCheckedAt),
			check.ExitIP,
			check.ExitCountry,
			check.ExitCountryCode,
			check.ExitRegion,
			time.Now().UTC().Format(time.RFC3339),
			check.NodeID,
		)
		if err != nil {
			return fmt.Errorf("update node quality summary %d: %w", check.NodeID, err)
		}

		if _, err := txStore.conn().ExecContext(ctx, "DELETE FROM node_quality_checks WHERE node_id = ?", check.NodeID); err != nil {
			return fmt.Errorf("delete old node quality items %d: %w", check.NodeID, err)
		}

		for _, item := range check.Items {
			if _, err := txStore.conn().ExecContext(ctx,
				`INSERT INTO node_quality_checks (node_id, target, status, http_status, latency_ms, message, checked_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				check.NodeID,
				item.Target,
				item.Status,
				item.HTTPStatus,
				item.LatencyMs,
				item.Message,
				formatTime(check.QualityCheckedAt),
			); err != nil {
				return fmt.Errorf("insert node quality item %d/%s: %w", check.NodeID, item.Target, err)
			}
		}

		return nil
	}

	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

// ===================== Timeline =====================

func (s *sqliteStore) AppendTimeline(ctx context.Context, nodeID int64, event TimelineEvent) error {
	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO node_timeline (node_id, success, latency_ms, error, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		nodeID, boolToInt(event.Success), event.LatencyMs, event.Error,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *sqliteStore) GetTimeline(ctx context.Context, nodeID int64, limit int) ([]TimelineEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.conn().QueryContext(ctx,
		`SELECT id, node_id, success, latency_ms, error, created_at
		 FROM node_timeline WHERE node_id = ?
		 ORDER BY id DESC LIMIT ?`,
		nodeID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get timeline for node %d: %w", nodeID, err)
	}
	defer rows.Close()

	var events []TimelineEvent
	for rows.Next() {
		var evt TimelineEvent
		var success int
		var createdAtStr string
		err := rows.Scan(&evt.ID, &evt.NodeID, &success, &evt.LatencyMs, &evt.Error, &createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("scan timeline event: %w", err)
		}
		evt.Success = success != 0
		evt.CreatedAt = parseTime(createdAtStr)
		events = append(events, evt)
	}

	// Reverse to get chronological order
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, rows.Err()
}

func (s *sqliteStore) CleanupTimeline(ctx context.Context, keepPerNode int) error {
	if keepPerNode <= 0 {
		keepPerNode = 20
	}

	_, err := s.conn().ExecContext(ctx,
		`DELETE FROM node_timeline WHERE id NOT IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY id DESC) as rn
				FROM node_timeline
			) WHERE rn <= ?
		)`, keepPerNode,
	)
	return err
}

// ===================== Sessions =====================

func (s *sqliteStore) CreateSession(ctx context.Context, session *Session) error {
	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO sessions (token, created_at, expires_at) VALUES (?, ?, ?)`,
		session.Token,
		session.CreatedAt.UTC().Format(time.RFC3339),
		session.ExpiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *sqliteStore) GetSession(ctx context.Context, token string) (*Session, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT token, created_at, expires_at FROM sessions WHERE token = ?", token)

	var sess Session
	var createdAtStr, expiresAtStr string
	err := row.Scan(&sess.Token, &createdAtStr, &expiresAtStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	sess.CreatedAt = parseTime(createdAtStr)
	sess.ExpiresAt = parseTime(expiresAtStr)
	return &sess, nil
}

func (s *sqliteStore) DeleteSession(ctx context.Context, token string) error {
	_, err := s.conn().ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
	return err
}

func (s *sqliteStore) CleanupExpiredSessions(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now)
	return err
}

// ===================== Subscription status =====================

func (s *sqliteStore) GetSubscriptionStatus(ctx context.Context) (*SubscriptionStatus, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT last_refresh, next_refresh, node_count, last_error,
		 refresh_count, is_refreshing, nodes_hash, updated_at
		 FROM subscription_status WHERE id = 1`)

	var status SubscriptionStatus
	var lastRefreshStr, nextRefreshStr, updatedAtStr string
	var isRefreshing int

	err := row.Scan(
		&lastRefreshStr, &nextRefreshStr, &status.NodeCount,
		&status.LastError, &status.RefreshCount, &isRefreshing,
		&status.NodesHash, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return &SubscriptionStatus{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription status: %w", err)
	}

	status.IsRefreshing = isRefreshing != 0
	status.LastRefresh = parseTime(lastRefreshStr)
	status.NextRefresh = parseTime(nextRefreshStr)
	status.UpdatedAt = parseTime(updatedAtStr)

	return &status, nil
}

func (s *sqliteStore) UpdateSubscriptionStatus(ctx context.Context, status *SubscriptionStatus) error {
	now := time.Now().UTC().Format(time.RFC3339)
	isRefreshing := 0
	if status.IsRefreshing {
		isRefreshing = 1
	}

	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO subscription_status (id, last_refresh, next_refresh, node_count, last_error,
		 refresh_count, is_refreshing, nodes_hash, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   last_refresh=excluded.last_refresh, next_refresh=excluded.next_refresh,
		   node_count=excluded.node_count, last_error=excluded.last_error,
		   refresh_count=excluded.refresh_count, is_refreshing=excluded.is_refreshing,
		   nodes_hash=excluded.nodes_hash, updated_at=excluded.updated_at`,
		formatTime(status.LastRefresh), formatTime(status.NextRefresh),
		status.NodeCount, status.LastError, status.RefreshCount,
		isRefreshing, status.NodesHash, now,
	)
	return err
}

// ===================== Lifecycle =====================

func (s *sqliteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *sqliteStore) WithTx(ctx context.Context, fn func(tx Store) error) error {
	if s.tx != nil {
		// Already in a transaction, just execute
		return fn(s)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	txStore := &sqliteStore{db: s.db, tx: tx}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ===================== Helpers =====================

func scanNode(row *sql.Row) (*Node, error) {
	var n Node
	var enabled int
	var createdAtStr, updatedAtStr string

	err := row.Scan(
		&n.ID, &n.URI, &n.Name, &n.Source, &n.FeedKey, &n.Port,
		&n.Username, &n.Password, &n.Region, &n.Country,
		&enabled, &n.LifecycleState, &createdAtStr, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	n.LifecycleState = normalizeNodeLifecycle(n.LifecycleState, enabled != 0)
	n.Enabled = lifecycleStateEnabled(n.LifecycleState)
	n.CreatedAt = parseTime(createdAtStr)
	n.UpdatedAt = parseTime(updatedAtStr)
	return &n, nil
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	var nodes []Node
	for rows.Next() {
		var n Node
		var enabled int
		var createdAtStr, updatedAtStr string

		err := rows.Scan(
			&n.ID, &n.URI, &n.Name, &n.Source, &n.FeedKey, &n.Port,
			&n.Username, &n.Password, &n.Region, &n.Country,
			&enabled, &n.LifecycleState, &createdAtStr, &updatedAtStr,
		)
		if err != nil {
			return nil, err
		}

		n.LifecycleState = normalizeNodeLifecycle(n.LifecycleState, enabled != 0)
		n.Enabled = lifecycleStateEnabled(n.LifecycleState)
		n.CreatedAt = parseTime(createdAtStr)
		n.UpdatedAt = parseTime(updatedAtStr)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *sqliteStore) BatchUpdateNodeLifecycle(ctx context.Context, nodeIDs []int64, lifecycleState string) error {
	if len(nodeIDs) == 0 {
		return nil
	}

	lifecycleState = normalizeNodeLifecycle(lifecycleState, true)
	enabled := boolToInt(lifecycleStateEnabled(lifecycleState))
	placeholders := make([]string, 0, len(nodeIDs))
	args := make([]any, 0, len(nodeIDs)+3)
	args = append(args, lifecycleState, enabled, time.Now().UTC().Format(time.RFC3339))
	for _, nodeID := range nodeIDs {
		placeholders = append(placeholders, "?")
		args = append(args, nodeID)
	}

	_, err := s.conn().ExecContext(ctx,
		fmt.Sprintf(
			`UPDATE nodes
			    SET lifecycle_state = ?, enabled = ?, updated_at = ?
			  WHERE id IN (%s)`,
			strings.Join(placeholders, ", "),
		),
		args...,
	)
	if err != nil {
		return fmt.Errorf("batch update node lifecycle: %w", err)
	}
	return nil
}

func (s *sqliteStore) GetNodeManualProbeResult(ctx context.Context, nodeID int64) (*NodeManualProbeResult, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT node_id, status, latency_ms, timed_out, message, checked_at, updated_at
		   FROM node_manual_probe_results
		  WHERE node_id = ?`,
		nodeID,
	)

	var result NodeManualProbeResult
	var timedOut int
	var checkedAtStr, updatedAtStr string
	err := row.Scan(
		&result.NodeID,
		&result.Status,
		&result.LatencyMs,
		&timedOut,
		&result.Message,
		&checkedAtStr,
		&updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get node manual probe result %d: %w", nodeID, err)
	}

	result.TimedOut = timedOut != 0
	result.CheckedAt = parseTime(checkedAtStr)
	result.UpdatedAt = parseTime(updatedAtStr)
	return &result, nil
}

func (s *sqliteStore) GetAllNodeManualProbeResults(ctx context.Context) (map[int64]*NodeManualProbeResult, error) {
	rows, err := s.conn().QueryContext(ctx,
		`SELECT node_id, status, latency_ms, timed_out, message, checked_at, updated_at
		   FROM node_manual_probe_results`,
	)
	if err != nil {
		return nil, fmt.Errorf("get all node manual probe results: %w", err)
	}
	defer rows.Close()

	results := make(map[int64]*NodeManualProbeResult)
	for rows.Next() {
		var result NodeManualProbeResult
		var timedOut int
		var checkedAtStr, updatedAtStr string
		if err := rows.Scan(
			&result.NodeID,
			&result.Status,
			&result.LatencyMs,
			&timedOut,
			&result.Message,
			&checkedAtStr,
			&updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan node manual probe result: %w", err)
		}
		result.TimedOut = timedOut != 0
		result.CheckedAt = parseTime(checkedAtStr)
		result.UpdatedAt = parseTime(updatedAtStr)
		copied := result
		results[result.NodeID] = &copied
	}
	return results, rows.Err()
}

func (s *sqliteStore) SaveNodeManualProbeResult(ctx context.Context, result *NodeManualProbeResult) error {
	if result == nil {
		return nil
	}

	if result.Status == "" {
		result.Status = ManualProbeStatusUntested
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO node_manual_probe_results (node_id, status, latency_ms, timed_out, message, checked_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
		   status=excluded.status,
		   latency_ms=excluded.latency_ms,
		   timed_out=excluded.timed_out,
		   message=excluded.message,
		   checked_at=excluded.checked_at,
		   updated_at=excluded.updated_at`,
		result.NodeID,
		result.Status,
		result.LatencyMs,
		boolToInt(result.TimedOut),
		result.Message,
		formatTime(result.CheckedAt),
		now,
	)
	if err != nil {
		return fmt.Errorf("save node manual probe result %d: %w", result.NodeID, err)
	}
	return nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try other formats
		t, err = time.Parse("2006-01-02 15:04:05", s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func normalizeNodeLifecycle(lifecycleState string, enabled bool) string {
	switch strings.TrimSpace(lifecycleState) {
	case NodeLifecycleActive:
		return NodeLifecycleActive
	case NodeLifecycleStaged:
		return NodeLifecycleStaged
	case NodeLifecycleDisabled:
		return NodeLifecycleDisabled
	default:
		if enabled {
			return NodeLifecycleActive
		}
		return NodeLifecycleDisabled
	}
}

func lifecycleStateEnabled(lifecycleState string) bool {
	switch strings.TrimSpace(lifecycleState) {
	case NodeLifecycleActive, NodeLifecycleStaged:
		return true
	default:
		return false
	}
}
