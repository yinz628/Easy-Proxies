package monitor

import (
	"testing"

	"easy_proxies/internal/config"
)

func TestBuildManageRowsMergesConfigAndRuntimeState(t *testing.T) {
	score := 88
	checked := int64(1712000000)
	rows := BuildManageRows(
		[]config.NodeConfig{
			{
				Name:           "node-normal",
				URI:            "socks5://1.1.1.1:1080",
				Port:           1080,
				Username:       "u1",
				Password:       "p1",
				Source:         config.NodeSourceManual,
				QualityStatus:  "healthy",
				QualityScore:   &score,
				QualityGrade:   "A",
				QualitySummary: "fast and stable",
				QualityChecked: &checked,
				ExitIP:         "8.8.8.8",
				ExitCountry:    "Hong Kong",
				ExitCountryCode:"HK",
				ExitRegion:     "HK",
			},
			{
				Name:     "node-disabled",
				URI:      "socks5://2.2.2.2:1080",
				Port:     2080,
				Source:   config.NodeSourceSubscription,
				Disabled: true,
			},
			{
				Name:   "node-pending",
				URI:    "socks5://3.3.3.3:1080",
				Port:   3080,
				Source: config.NodeSourceManual,
			},
		},
		[]Snapshot{
			{
				NodeInfo: NodeInfo{
					Tag:     "tag-normal",
					Name:    "runtime-normal",
					URI:     "socks5://1.1.1.1:1080",
					Port:    1080,
					Region:  "hk",
					Country: "Hong Kong",
				},
				LastLatencyMs:    42,
				Available:        true,
				InitialCheckDone: true,
				SuccessCount:     7,
				FailureCount:     1,
			},
			{
				NodeInfo: NodeInfo{
					Tag:     "tag-pending",
					Name:    "node-pending",
					URI:     "socks5://3.3.3.3:1080",
					Port:    3080,
					Region:  "jp",
					Country: "Japan",
				},
				LastLatencyMs:    -1,
				Available:        false,
				InitialCheckDone: false,
			},
		},
	)

	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}

	byName := make(map[string]ManageRow, len(rows))
	for _, row := range rows {
		byName[row.Name] = row
	}

	normal := byName["node-normal"]
	if normal.RuntimeStatus != "normal" {
		t.Fatalf("normal.RuntimeStatus = %q, want normal", normal.RuntimeStatus)
	}
	if normal.Tag != "tag-normal" {
		t.Fatalf("normal.Tag = %q, want tag-normal", normal.Tag)
	}
	if normal.Source != string(config.NodeSourceManual) {
		t.Fatalf("normal.Source = %q, want %q", normal.Source, config.NodeSourceManual)
	}
	if normal.QualityStatus != "healthy" || normal.QualityGrade != "A" || normal.QualitySummary != "fast and stable" {
		t.Fatalf("normal quality fields = %#v", normal)
	}
	if normal.QualityScore == nil || *normal.QualityScore != 88 {
		t.Fatalf("normal.QualityScore = %#v, want 88", normal.QualityScore)
	}
	if normal.ExitCountryCode != "HK" || normal.ExitRegion != "HK" {
		t.Fatalf("normal exit fields = %#v", normal)
	}

	disabled := byName["node-disabled"]
	if disabled.RuntimeStatus != "disabled" {
		t.Fatalf("disabled.RuntimeStatus = %q, want disabled", disabled.RuntimeStatus)
	}
	if disabled.Tag != "" {
		t.Fatalf("disabled.Tag = %q, want empty", disabled.Tag)
	}

	pending := byName["node-pending"]
	if pending.RuntimeStatus != "pending" {
		t.Fatalf("pending.RuntimeStatus = %q, want pending", pending.RuntimeStatus)
	}
	if pending.Tag != "tag-pending" {
		t.Fatalf("pending.Tag = %q, want tag-pending", pending.Tag)
	}
}

func TestQueryManageRowsFiltersSortsAndPaginates(t *testing.T) {
	rows := []ManageRow{
		{Name: "alpha-hk", URI: "trojan://alpha", Source: "manual", RuntimeStatus: "normal", Region: "hk", Country: "Hong Kong", LatencyMS: 40, Port: 1001},
		{Name: "beta-us", URI: "trojan://beta", Source: "manual", RuntimeStatus: "normal", Region: "us", Country: "United States", LatencyMS: 15, Port: 1002},
		{Name: "gamma-hk", URI: "trojan://gamma", Source: "subscription", RuntimeStatus: "unavailable", Region: "hk", Country: "Hong Kong", LatencyMS: 80, Port: 1003},
		{Name: "delta-hk", URI: "trojan://delta", Source: "manual", RuntimeStatus: "normal", Region: "hk", Country: "Hong Kong", LatencyMS: 25, Port: 1004},
	}

	result := QueryManageRows(rows, ManageQuery{
		Page:     2,
		PageSize: 1,
		Keyword:  "hk",
		Status:   "normal",
		Region:   "hk",
		Source:   "manual",
		SortKey:  ManageSortByLatency,
		SortDir:  "asc",
	})

	if result.Total != 4 {
		t.Fatalf("result.Total = %d, want 4", result.Total)
	}
	if result.FilteredTotal != 2 {
		t.Fatalf("result.FilteredTotal = %d, want 2", result.FilteredTotal)
	}
	if result.Page != 2 || result.PageSize != 1 {
		t.Fatalf("page info = (%d,%d), want (2,1)", result.Page, result.PageSize)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(result.Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].Name != "alpha-hk" {
		t.Fatalf("result.Items[0].Name = %q, want alpha-hk", result.Items[0].Name)
	}
	if result.Summary["normal"] != 3 || result.Summary["unavailable"] != 1 {
		t.Fatalf("summary = %#v", result.Summary)
	}
	if len(result.Facets.Regions) != 2 || result.Facets.Regions[0] != "hk" || result.Facets.Regions[1] != "us" {
		t.Fatalf("regions = %#v", result.Facets.Regions)
	}
	if len(result.Facets.Sources) != 2 || result.Facets.Sources[0] != "manual" || result.Facets.Sources[1] != "subscription" {
		t.Fatalf("sources = %#v", result.Facets.Sources)
	}
}

func TestResolveManageSelectionSupportsNamesAndFilterMode(t *testing.T) {
	rows := []ManageRow{
		{Name: "alpha", RuntimeStatus: "normal", Region: "hk", Source: "manual", Tag: "tag-alpha"},
		{Name: "beta", RuntimeStatus: "normal", Region: "hk", Source: "manual", Tag: "tag-beta"},
		{Name: "gamma", RuntimeStatus: "unavailable", Region: "us", Source: "subscription", Tag: "tag-gamma"},
	}

	byNames, err := ResolveManageSelection(rows, ManageSelection{
		Mode:  "names",
		Names: []string{"beta", "missing", "alpha"},
	})
	if err != nil {
		t.Fatalf("ResolveManageSelection(names) error = %v", err)
	}
	if len(byNames) != 2 || byNames[0].Name != "alpha" || byNames[1].Name != "beta" {
		t.Fatalf("byNames = %#v", byNames)
	}

	byFilter, err := ResolveManageSelection(rows, ManageSelection{
		Mode: "filter",
		Filter: ManageQuery{
			Status: "normal",
			Region: "hk",
			Source: "manual",
		},
		ExcludeNames: []string{"beta"},
	})
	if err != nil {
		t.Fatalf("ResolveManageSelection(filter) error = %v", err)
	}
	if len(byFilter) != 1 || byFilter[0].Name != "alpha" {
		t.Fatalf("byFilter = %#v", byFilter)
	}
}
