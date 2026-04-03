package monitor

import (
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

func TestBuildManageRowsMergesConfigAndRuntimeState(t *testing.T) {
	score := 88
	checked := int64(1712000000)
	rows := BuildManageRows(
		[]config.NodeConfig{
			{
				Name:                   "node-normal",
				URI:                    "socks5://1.1.1.1:1080",
				Port:                   1080,
				Username:               "u1",
				Password:               "p1",
				Source:                 config.NodeSourceManual,
				QualityVersion:         quality.QualityVersionAIReachabilityV2,
				QualityStatus:          quality.StatusOpenAIOnly,
				QualityOpenAIStatus:    quality.StatusPass,
				QualityAnthropicStatus: quality.StatusFail,
				QualityScore:           &score,
				QualityGrade:           "A",
				QualitySummary:         "fast and stable",
				QualityChecked:         &checked,
				ExitIP:                 "8.8.8.8",
				ExitCountry:            "Hong Kong",
				ExitCountryCode:        "HK",
				ExitRegion:             "HK",
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
	if normal.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("normal.QualityVersion = %q, want %q", normal.QualityVersion, quality.QualityVersionAIReachabilityV2)
	}
	if normal.QualityStatus != quality.StatusOpenAIOnly || normal.QualityGrade != "A" || normal.QualitySummary != "fast and stable" {
		t.Fatalf("normal quality fields = %#v", normal)
	}
	if normal.QualityOpenAIStatus != quality.StatusPass || normal.QualityAnthropicStatus != quality.StatusFail {
		t.Fatalf("normal provider quality fields = %#v", normal)
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

func TestBuildManageRowsTreatsLegacyQualityStatusAsUnchecked(t *testing.T) {
	rows := BuildManageRows(
		[]config.NodeConfig{
			{
				Name:            "legacy-node",
				URI:             "socks5://4.4.4.4:1080",
				Source:          config.NodeSourceManual,
				QualityStatus:   "healthy",
				QualityGrade:    "A",
				QualitySummary:  "legacy aggregate",
				ExitIP:          "9.9.9.9",
				ExitCountry:     "Singapore",
				ExitCountryCode: "SG",
				ExitRegion:      "SG",
			},
			{
				Name:                   "current-node",
				URI:                    "socks5://5.5.5.5:1080",
				Source:                 config.NodeSourceManual,
				QualityVersion:         quality.QualityVersionAIReachabilityV2,
				QualityStatus:          quality.StatusDualAvailable,
				QualityOpenAIStatus:    quality.StatusPass,
				QualityAnthropicStatus: quality.StatusPass,
				QualityGrade:           "A",
				QualitySummary:         "OpenAI 可用，Anthropic 可用",
			},
		},
		nil,
	)

	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}

	byName := make(map[string]ManageRow, len(rows))
	for _, row := range rows {
		byName[row.Name] = row
	}

	legacy := byName["legacy-node"]
	if legacy.QualityStatus != "unchecked" {
		t.Fatalf("legacy.QualityStatus = %q, want unchecked", legacy.QualityStatus)
	}
	if legacy.QualityVersion != "" {
		t.Fatalf("legacy.QualityVersion = %q, want empty", legacy.QualityVersion)
	}
	if legacy.QualityOpenAIStatus != "" || legacy.QualityAnthropicStatus != "" {
		t.Fatalf("legacy provider statuses = (%q,%q), want empty", legacy.QualityOpenAIStatus, legacy.QualityAnthropicStatus)
	}
	if legacy.QualityScore != nil || legacy.QualityGrade != "" || legacy.QualitySummary != "" || legacy.QualityChecked != nil {
		t.Fatalf("legacy aggregate fields should be cleared, got %#v", legacy)
	}
	if legacy.ExitIP != "" || legacy.ExitCountry != "" || legacy.ExitCountryCode != "" || legacy.ExitRegion != "" {
		t.Fatalf("legacy exit fields should be cleared, got %#v", legacy)
	}

	current := byName["current-node"]
	if current.QualityVersion != quality.QualityVersionAIReachabilityV2 {
		t.Fatalf("current.QualityVersion = %q, want %q", current.QualityVersion, quality.QualityVersionAIReachabilityV2)
	}
	if current.QualityStatus != quality.StatusDualAvailable {
		t.Fatalf("current.QualityStatus = %q, want %q", current.QualityStatus, quality.StatusDualAvailable)
	}
	if current.QualityOpenAIStatus != quality.StatusPass || current.QualityAnthropicStatus != quality.StatusPass {
		t.Fatalf("current provider statuses = (%q,%q), want pass/pass", current.QualityOpenAIStatus, current.QualityAnthropicStatus)
	}
}

func TestQueryManageRowsFiltersSortsAndPaginates(t *testing.T) {
	rows := []ManageRow{
		{Name: "alpha-hk", URI: "trojan://alpha", Source: "manual", RuntimeStatus: "normal", Region: "hk", Country: "Hong Kong", LatencyMS: 40, Port: 1001, QualityStatus: "healthy"},
		{Name: "beta-us", URI: "trojan://beta", Source: "manual", RuntimeStatus: "normal", Region: "us", Country: "United States", LatencyMS: 15, Port: 1002, QualityStatus: "warn"},
		{Name: "gamma-hk", URI: "trojan://gamma", Source: "subscription", RuntimeStatus: "unavailable", Region: "hk", Country: "Hong Kong", LatencyMS: 80, Port: 1003, QualityStatus: "fail"},
		{Name: "delta-hk", URI: "trojan://delta", Source: "manual", RuntimeStatus: "normal", Region: "hk", Country: "Hong Kong", LatencyMS: 25, Port: 1004, QualityStatus: "healthy"},
	}

	result := QueryManageRows(rows, ManageQuery{
		Page:          2,
		PageSize:      1,
		Keyword:       "hk",
		Status:        "normal",
		Region:        "hk",
		Source:        "manual",
		QualityStatus: "healthy",
		SortKey:       ManageSortByLatency,
		SortDir:       "asc",
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
	if len(result.Facets.QualityStatuses) != 3 || result.Facets.QualityStatuses[0] != "fail" || result.Facets.QualityStatuses[1] != "healthy" || result.Facets.QualityStatuses[2] != "warn" {
		t.Fatalf("quality statuses = %#v", result.Facets.QualityStatuses)
	}
}

func TestResolveManageSelectionSupportsNamesAndFilterMode(t *testing.T) {
	rows := []ManageRow{
		{Name: "alpha", RuntimeStatus: "normal", Region: "hk", Source: "manual", Tag: "tag-alpha", QualityStatus: "healthy"},
		{Name: "beta", RuntimeStatus: "normal", Region: "hk", Source: "manual", Tag: "tag-beta", QualityStatus: "healthy"},
		{Name: "gamma", RuntimeStatus: "unavailable", Region: "us", Source: "subscription", Tag: "tag-gamma", QualityStatus: "warn"},
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
			Status:        "normal",
			Region:        "hk",
			Source:        "manual",
			QualityStatus: "healthy",
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

func TestQueryManageRowsFiltersByQualityStatus(t *testing.T) {
	rows := []ManageRow{
		{Name: "alpha", RuntimeStatus: "normal", QualityStatus: "healthy"},
		{Name: "beta", RuntimeStatus: "normal", QualityStatus: "warn"},
		{Name: "gamma", RuntimeStatus: "pending", QualityStatus: ""},
	}

	result := QueryManageRows(rows, ManageQuery{
		QualityStatus: "warn",
	})

	if result.FilteredTotal != 1 {
		t.Fatalf("result.FilteredTotal = %d, want 1", result.FilteredTotal)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "beta" {
		t.Fatalf("result.Items = %#v, want beta", result.Items)
	}
}

func TestQueryManageRowsFiltersByLifecycleManualProbeAndActivationReady(t *testing.T) {
	rows := []ManageRow{
		{
			Name:                  "staged-ready",
			RuntimeStatus:         "pending",
			LifecycleState:        store.NodeLifecycleStaged,
			ManualProbeStatus:     store.ManualProbeStatusPass,
			ActivationReady:       true,
			ActivationBlockReason: "",
		},
		{
			Name:                  "staged-blocked",
			RuntimeStatus:         "pending",
			LifecycleState:        store.NodeLifecycleStaged,
			ManualProbeStatus:     store.ManualProbeStatusFail,
			ActivationReady:       false,
			ActivationBlockReason: "入池预检失败",
		},
		{
			Name:                  "active-ready",
			RuntimeStatus:         "normal",
			LifecycleState:        store.NodeLifecycleActive,
			ManualProbeStatus:     store.ManualProbeStatusPass,
			ActivationReady:       true,
			ActivationBlockReason: "",
		},
	}

	result := QueryManageRows(rows, ManageQuery{
		LifecycleState:   store.NodeLifecycleStaged,
		ManualProbeStatus: store.ManualProbeStatusPass,
		ActivationReady:  "ready",
	})

	if result.FilteredTotal != 1 {
		t.Fatalf("result.FilteredTotal = %d, want 1", result.FilteredTotal)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "staged-ready" {
		t.Fatalf("result.Items = %#v, want staged-ready", result.Items)
	}
	if len(result.Facets.LifecycleStates) != 2 {
		t.Fatalf("result.Facets.LifecycleStates = %#v, want 2 states", result.Facets.LifecycleStates)
	}
	if len(result.Facets.ManualProbeStatuses) != 2 {
		t.Fatalf("result.Facets.ManualProbeStatuses = %#v, want 2 statuses", result.Facets.ManualProbeStatuses)
	}
	if len(result.Facets.ActivationReadiness) != 2 {
		t.Fatalf("result.Facets.ActivationReadiness = %#v, want ready/blocked", result.Facets.ActivationReadiness)
	}
}

func TestApplyManageManualProbeResultsUsesManualProbeAsFallbackDisplayForPendingNodes(t *testing.T) {
	rows := BuildManageRows(
		[]config.NodeConfig{
			{
				Name:           "staged-pass",
				URI:            "socks5://1.1.1.1:1080",
				Source:         config.NodeSourceTXTSubscription,
				LifecycleState: store.NodeLifecycleStaged,
			},
			{
				Name:           "staged-timeout",
				URI:            "socks5://2.2.2.2:1080",
				Source:         config.NodeSourceTXTSubscription,
				LifecycleState: store.NodeLifecycleStaged,
			},
		},
		nil,
	)

	storeNodes := []store.Node{
		{ID: 1, Name: "staged-pass", URI: "socks5://1.1.1.1:1080", LifecycleState: store.NodeLifecycleStaged},
		{ID: 2, Name: "staged-timeout", URI: "socks5://2.2.2.2:1080", LifecycleState: store.NodeLifecycleStaged},
	}
	results := map[int64]*store.NodeManualProbeResult{
		1: {
			NodeID:    1,
			Status:    store.ManualProbeStatusPass,
			LatencyMs: 55,
		},
		2: {
			NodeID:    2,
			Status:    store.ManualProbeStatusTimeout,
			LatencyMs: 0,
			TimedOut:  true,
			Message:   "probe timeout after 10000ms",
		},
	}

	ApplyManageManualProbeResults(rows, storeNodes, results)

	byName := make(map[string]ManageRow, len(rows))
	for _, row := range rows {
		byName[row.Name] = row
	}

	passRow := byName["staged-pass"]
	if passRow.RuntimeStatus != "normal" {
		t.Fatalf("passRow.RuntimeStatus = %q, want normal", passRow.RuntimeStatus)
	}
	if passRow.LatencyMS != 55 {
		t.Fatalf("passRow.LatencyMS = %d, want 55", passRow.LatencyMS)
	}
	if passRow.ManualProbeStatus != store.ManualProbeStatusPass {
		t.Fatalf("passRow.ManualProbeStatus = %q, want pass", passRow.ManualProbeStatus)
	}

	timeoutRow := byName["staged-timeout"]
	if timeoutRow.RuntimeStatus != "unavailable" {
		t.Fatalf("timeoutRow.RuntimeStatus = %q, want unavailable", timeoutRow.RuntimeStatus)
	}
	if timeoutRow.LatencyMS != -1 {
		t.Fatalf("timeoutRow.LatencyMS = %d, want -1", timeoutRow.LatencyMS)
	}
	if timeoutRow.ManualProbeStatus != store.ManualProbeStatusTimeout {
		t.Fatalf("timeoutRow.ManualProbeStatus = %q, want timeout", timeoutRow.ManualProbeStatus)
	}
}

func TestApplyManageManualProbeResultsDoesNotOverrideExistingRuntimeDisplay(t *testing.T) {
	rows := BuildManageRows(
		[]config.NodeConfig{
			{
				Name:           "active-runtime",
				URI:            "socks5://3.3.3.3:1080",
				Source:         config.NodeSourceManual,
				LifecycleState: store.NodeLifecycleActive,
			},
		},
		[]Snapshot{
			{
				NodeInfo: NodeInfo{
					Tag:  "tag-runtime",
					Name: "active-runtime",
					URI:  "socks5://3.3.3.3:1080",
				},
				LastLatencyMs:    42,
				Available:        true,
				InitialCheckDone: true,
			},
		},
	)

	storeNodes := []store.Node{
		{ID: 3, Name: "active-runtime", URI: "socks5://3.3.3.3:1080", LifecycleState: store.NodeLifecycleActive},
	}
	results := map[int64]*store.NodeManualProbeResult{
		3: {
			NodeID:    3,
			Status:    store.ManualProbeStatusTimeout,
			LatencyMs: 0,
			TimedOut:  true,
			Message:   "probe timeout after 10000ms",
		},
	}

	ApplyManageManualProbeResults(rows, storeNodes, results)

	row := rows[0]
	if row.RuntimeStatus != "normal" {
		t.Fatalf("row.RuntimeStatus = %q, want normal", row.RuntimeStatus)
	}
	if row.LatencyMS != 42 {
		t.Fatalf("row.LatencyMS = %d, want 42", row.LatencyMS)
	}
	if row.ManualProbeStatus != store.ManualProbeStatusTimeout {
		t.Fatalf("row.ManualProbeStatus = %q, want timeout", row.ManualProbeStatus)
	}
}
