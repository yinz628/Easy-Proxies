package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"easy_proxies/internal/quality"

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
		"quality_version", "quality_openai_status", "quality_anthropic_status",
		"quality_checked_at", "exit_ip", "exit_country", "exit_country_code", "exit_region",
	} {
		if !columns[name] {
			t.Fatalf("node_stats missing column %q", name)
		}
	}
}

func TestMigrateAddsLifecycleStateAndManualProbeTable(t *testing.T) {
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

	rows, err := db.Query("PRAGMA table_info(nodes)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(nodes) error = %v", err)
	}
	defer rows.Close()

	nodeColumns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("rows.Scan(nodes) error = %v", err)
		}
		nodeColumns[name] = true
	}
	if !nodeColumns["lifecycle_state"] {
		t.Fatal("nodes missing column \"lifecycle_state\"")
	}

	manualProbeRows, err := db.Query("PRAGMA table_info(node_manual_probe_results)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(node_manual_probe_results) error = %v", err)
	}
	defer manualProbeRows.Close()

	manualProbeColumns := map[string]bool{}
	for manualProbeRows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := manualProbeRows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("rows.Scan(node_manual_probe_results) error = %v", err)
		}
		manualProbeColumns[name] = true
	}

	for _, name := range []string{
		"node_id",
		"status",
		"latency_ms",
		"timed_out",
		"message",
		"checked_at",
		"updated_at",
	} {
		if !manualProbeColumns[name] {
			t.Fatalf("node_manual_probe_results missing column %q", name)
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

func TestSaveNodeQualityCheckPersistsVersionAndProviderStatuses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	node := &Node{
		URI:     "http://3.3.3.3:80",
		Name:    "quality-version-node",
		Source:  NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	checkedAt := time.Now().UTC().Truncate(time.Second)
	check := &NodeQualityCheck{
		NodeID:                 node.ID,
		QualityVersion:         quality.QualityVersionAIReachabilityV2,
		QualityStatus:          quality.StatusOpenAIOnly,
		QualityOpenAIStatus:    quality.StatusPass,
		QualityAnthropicStatus: quality.StatusFail,
		QualityScore:           intPtr(70),
		QualityGrade:           "B",
		QualitySummary:         "OpenAI 可用，Anthropic 不可用",
		QualityCheckedAt:       checkedAt,
		Items: []NodeQualityCheckItem{
			{Target: quality.TargetOpenAIReachability, Status: quality.StatusPass, HTTPStatus: 401, LatencyMs: 120},
			{Target: quality.TargetAnthropicReachability, Status: quality.StatusFail, HTTPStatus: 405, LatencyMs: 140},
		},
	}
	if err := st.SaveNodeQualityCheck(ctx, check); err != nil {
		t.Fatalf("SaveNodeQualityCheck() error = %v", err)
	}

	gotCheck, err := st.GetNodeQualityCheck(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeQualityCheck() error = %v", err)
	}
	if gotCheck == nil {
		t.Fatal("GetNodeQualityCheck() = nil, want non-nil")
	}
	if gotCheck.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("QualityVersion = %q, want %q", gotCheck.QualityVersion, quality.QualityVersionAIReachabilityV2)
	}
	if gotCheck.QualityOpenAIStatus != quality.StatusPass {
		t.Fatalf("QualityOpenAIStatus = %q, want %q", gotCheck.QualityOpenAIStatus, quality.StatusPass)
	}
	if gotCheck.QualityAnthropicStatus != quality.StatusFail {
		t.Fatalf("QualityAnthropicStatus = %q, want %q", gotCheck.QualityAnthropicStatus, quality.StatusFail)
	}

	gotStats, err := st.GetNodeStats(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNodeStats() error = %v", err)
	}
	if gotStats == nil {
		t.Fatal("GetNodeStats() = nil, want non-nil")
	}
	if gotStats.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("stats.QualityVersion = %q, want %q", gotStats.QualityVersion, quality.QualityVersionAIReachabilityV2)
	}
	if gotStats.QualityOpenAIStatus != quality.StatusPass {
		t.Fatalf("stats.QualityOpenAIStatus = %q, want %q", gotStats.QualityOpenAIStatus, quality.StatusPass)
	}
	if gotStats.QualityAnthropicStatus != quality.StatusFail {
		t.Fatalf("stats.QualityAnthropicStatus = %q, want %q", gotStats.QualityAnthropicStatus, quality.StatusFail)
	}

	allStats, err := st.GetAllNodeStats(ctx)
	if err != nil {
		t.Fatalf("GetAllNodeStats() error = %v", err)
	}
	entry := allStats[node.ID]
	if entry == nil {
		t.Fatalf("GetAllNodeStats()[%d] = nil, want non-nil", node.ID)
	}
	if entry.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("allStats.QualityVersion = %q, want %q", entry.QualityVersion, quality.QualityVersionAIReachabilityV2)
	}
	if entry.QualityOpenAIStatus != quality.StatusPass {
		t.Fatalf("allStats.QualityOpenAIStatus = %q, want %q", entry.QualityOpenAIStatus, quality.StatusPass)
	}
	if entry.QualityAnthropicStatus != quality.StatusFail {
		t.Fatalf("allStats.QualityAnthropicStatus = %q, want %q", entry.QualityAnthropicStatus, quality.StatusFail)
	}
}

func intPtr(v int) *int {
	return &v
}
