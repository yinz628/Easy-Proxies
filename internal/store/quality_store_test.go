package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigrateAddsQualitySummaryColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	rows, err := db.Query("PRAGMA table_info(node_stats)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(node_stats) error = %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		columns[name] = true
	}

	for _, name := range []string{
		"quality_status", "quality_score", "quality_grade", "quality_summary",
		"quality_checked_at", "exit_ip", "exit_country", "exit_country_code", "exit_region",
	} {
		if !columns[name] {
			t.Fatalf("node_stats missing column %q", name)
		}
	}
}

func TestSaveNodeQualityCheckReplacesPreviousItems(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &Node{
		URI:     "http://1.1.1.1:80",
		Name:    "quality-node",
		Source:  NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	first := &NodeQualityCheck{
		NodeID:           node.ID,
		QualityStatus:    "healthy",
		QualityScore:     intPtr(90),
		QualityGrade:     "A",
		QualitySummary:   "first",
		QualityCheckedAt: time.Now(),
		ExitIP:           "1.1.1.1",
		ExitCountry:      "US",
		ExitCountryCode:  "US",
		ExitRegion:       "California",
		Items: []NodeQualityCheckItem{
			{Target: "base_connectivity", Status: "pass", HTTPStatus: 200, LatencyMs: 100, Message: "ok"},
			{Target: "openai", Status: "warn", HTTPStatus: 401, LatencyMs: 120, Message: "reachable"},
		},
	}
	if err := st.SaveNodeQualityCheck(ctx, first); err != nil {
		t.Fatalf("SaveNodeQualityCheck(first) error = %v", err)
	}

	second := &NodeQualityCheck{
		NodeID:           node.ID,
		QualityStatus:    "failed",
		QualityScore:     intPtr(40),
		QualityGrade:     "D",
		QualitySummary:   "second",
		QualityCheckedAt: time.Now(),
		ExitIP:           "2.2.2.2",
		ExitCountry:      "JP",
		ExitCountryCode:  "JP",
		ExitRegion:       "Tokyo",
		Items: []NodeQualityCheckItem{
			{Target: "base_connectivity", Status: "pass", HTTPStatus: 200, LatencyMs: 150, Message: "ok"},
		},
	}
	if err := st.SaveNodeQualityCheck(ctx, second); err != nil {
		t.Fatalf("SaveNodeQualityCheck(second) error = %v", err)
	}

	got, err := st.GetNodeQualityCheck(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeQualityCheck() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetNodeQualityCheck() = nil, want non-nil")
	}

	if got.QualitySummary != "second" {
		t.Fatalf("QualitySummary = %q, want %q", got.QualitySummary, "second")
	}
	if len(got.Items) != 1 {
		t.Fatalf("len(got.Items) = %d, want 1", len(got.Items))
	}
	if got.Items[0].Target != "base_connectivity" {
		t.Fatalf("Items[0].Target = %q, want %q", got.Items[0].Target, "base_connectivity")
	}
}

func intPtr(v int) *int {
	return &v
}
