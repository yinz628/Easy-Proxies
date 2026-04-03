package monitor

import "testing"

func TestResolveImportedNodeNameUsesNextAvailablePrefixedName(t *testing.T) {
	reserved := map[string]struct{}{
		"imported-1": {},
		"imported-2": {},
	}
	counters := map[string]int{
		"imported": 2,
	}

	finalName, renamed, reason := resolveImportedNodeName("", "imported", reserved, counters)

	if finalName != "imported-3" {
		t.Fatalf("finalName = %q, want %q", finalName, "imported-3")
	}
	if !renamed {
		t.Fatal("renamed = false, want true")
	}
	if reason != "missing_name" {
		t.Fatalf("reason = %q, want %q", reason, "missing_name")
	}
}

func TestResolveImportedNodeNameFallsBackWhenOriginalNameConflicts(t *testing.T) {
	reserved := map[string]struct{}{
		"US-A": {},
		"hk-7": {},
	}
	counters := map[string]int{
		"hk": 7,
	}

	finalName, renamed, reason := resolveImportedNodeName("US-A", "hk", reserved, counters)

	if finalName != "hk-8" {
		t.Fatalf("finalName = %q, want %q", finalName, "hk-8")
	}
	if !renamed {
		t.Fatal("renamed = false, want true")
	}
	if reason != "name_conflict" {
		t.Fatalf("reason = %q, want %q", reason, "name_conflict")
	}
}

func TestResolveImportedNodeNameKeepsOriginalNameWhenAvailable(t *testing.T) {
	reserved := map[string]struct{}{
		"hk-7": {},
	}
	counters := map[string]int{
		"hk": 7,
	}

	finalName, renamed, reason := resolveImportedNodeName("US-A", "hk", reserved, counters)

	if finalName != "US-A" {
		t.Fatalf("finalName = %q, want %q", finalName, "US-A")
	}
	if renamed {
		t.Fatal("renamed = true, want false")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}
