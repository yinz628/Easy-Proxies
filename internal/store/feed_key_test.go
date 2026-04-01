package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateAddsFeedKeyColumn(t *testing.T) {
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

	foundFeedKey := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		if name == "feed_key" {
			foundFeedKey = true
			break
		}
	}

	if !foundFeedKey {
		t.Fatal("nodes table is missing feed_key column")
	}
}

func TestDeleteNodesByFeedKeyOnlyRemovesMatchingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.CreateNode(ctx, &Node{
		URI:     "http://1.1.1.1:80",
		Name:    "feed-a-node",
		Source:  NodeSourceTXTSubscription,
		FeedKey: "txt:https://example.com/a.txt",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateNode(feed-a) error = %v", err)
	}

	if err := st.CreateNode(ctx, &Node{
		URI:     "http://2.2.2.2:80",
		Name:    "feed-b-node",
		Source:  NodeSourceTXTSubscription,
		FeedKey: "txt:https://example.com/b.txt",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateNode(feed-b) error = %v", err)
	}

	deleted, err := st.DeleteNodesByFeedKey(ctx, "txt:https://example.com/a.txt")
	if err != nil {
		t.Fatalf("DeleteNodesByFeedKey() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteNodesByFeedKey() deleted %d rows, want 1", deleted)
	}

	nodes, err := st.ListNodes(ctx, NodeFilter{})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if got, want := nodes[0].FeedKey, "txt:https://example.com/b.txt"; got != want {
		t.Fatalf("remaining node FeedKey = %q, want %q", got, want)
	}
}

func TestReplaceTXTFeedNodesDeduplicatesAcrossFeeds(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.ReplaceTXTFeedNodes(ctx, "txt:https://example.com/a.txt", []Node{
		{URI: "http://1.1.1.1:80", Name: "a-1", Source: NodeSourceTXTSubscription, Enabled: true},
		{URI: "http://2.2.2.2:80", Name: "a-2", Source: NodeSourceTXTSubscription, Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceTXTFeedNodes(feed A) error = %v", err)
	}

	if err := st.ReplaceTXTFeedNodes(ctx, "txt:https://example.com/b.txt", []Node{
		{URI: "http://1.1.1.1:80", Name: "b-1", Source: NodeSourceTXTSubscription, Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceTXTFeedNodes(feed B) error = %v", err)
	}

	nodes, err := st.ListNodes(ctx, NodeFilter{Source: NodeSourceTXTSubscription})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(nodes))
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var membershipCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM txt_feed_memberships WHERE uri = ?`, "http://1.1.1.1:80").Scan(&membershipCount); err != nil {
		t.Fatalf("QueryRow(membershipCount) error = %v", err)
	}
	if membershipCount != 2 {
		t.Fatalf("membershipCount = %d, want 2", membershipCount)
	}
}

func TestReplaceTXTFeedNodesKeepsSharedURIReferencedByAnotherFeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.ReplaceTXTFeedNodes(ctx, "txt:https://example.com/a.txt", []Node{
		{URI: "http://1.1.1.1:80", Name: "a-1", Source: NodeSourceTXTSubscription, Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceTXTFeedNodes(feed A initial) error = %v", err)
	}
	if err := st.ReplaceTXTFeedNodes(ctx, "txt:https://example.com/b.txt", []Node{
		{URI: "http://1.1.1.1:80", Name: "b-1", Source: NodeSourceTXTSubscription, Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceTXTFeedNodes(feed B initial) error = %v", err)
	}

	if err := st.ReplaceTXTFeedNodes(ctx, "txt:https://example.com/a.txt", []Node{}); err != nil {
		t.Fatalf("ReplaceTXTFeedNodes(feed A replace empty) error = %v", err)
	}

	nodes, err := st.ListNodes(ctx, NodeFilter{Source: NodeSourceTXTSubscription})
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if got, want := nodes[0].URI, "http://1.1.1.1:80"; got != want {
		t.Fatalf("remaining node URI = %q, want %q", got, want)
	}
}
