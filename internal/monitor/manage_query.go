package monitor

import (
	"errors"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

type ManageSortKey string

const (
	ManageSortByName    ManageSortKey = "name"
	ManageSortByStatus  ManageSortKey = "status"
	ManageSortByLatency ManageSortKey = "latency"
	ManageSortByRegion  ManageSortKey = "region"
	ManageSortByPort    ManageSortKey = "port"
	ManageSortBySource  ManageSortKey = "source"
)

const (
	defaultManagePage            = 1
	defaultManagePageSize        = 100
	maxManagePageSize            = 500
	manageQualityStatusUnchecked = "unchecked"
)

var errInvalidManageSelection = errors.New("invalid manage selection")

type ManageQuery struct {
	Page              int
	PageSize          int
	Keyword           string
	Status            string
	Region            string
	Source            string
	LifecycleState    string
	ManualProbeStatus string
	ActivationReady   string
	QualityStatus     string
	SortKey           ManageSortKey
	SortDir           string
}

type ManageRow struct {
	Name                   string `json:"name"`
	URI                    string `json:"uri"`
	Port                   uint16 `json:"port"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	Source                 string `json:"source,omitempty"`
	Disabled               bool   `json:"disabled,omitempty"`
	LifecycleState         string `json:"lifecycle_state,omitempty"`
	RuntimeStatus          string `json:"runtime_status"`
	Tag                    string `json:"tag,omitempty"`
	LatencyMS              int64  `json:"latency_ms"`
	ManualProbeStatus      string `json:"manual_probe_status"`
	ManualProbeLatencyMS   int64  `json:"manual_probe_latency_ms"`
	ManualProbeChecked     *int64 `json:"manual_probe_checked,omitempty"`
	ManualProbeMessage     string `json:"manual_probe_message,omitempty"`
	ActivationReady        bool   `json:"activation_ready"`
	ActivationBlockReason  string `json:"activation_block_reason,omitempty"`
	Region                 string `json:"region,omitempty"`
	Country                string `json:"country,omitempty"`
	ActiveConnections      int    `json:"active_connections"`
	SuccessCount           int64  `json:"success_count"`
	FailureCount           int    `json:"failure_count"`
	QualityVersion         string `json:"quality_version,omitempty"`
	QualityStatus          string `json:"quality_status,omitempty"`
	QualityOpenAIStatus    string `json:"quality_openai_status,omitempty"`
	QualityAnthropicStatus string `json:"quality_anthropic_status,omitempty"`
	QualityScore           *int   `json:"quality_score,omitempty"`
	QualityGrade           string `json:"quality_grade,omitempty"`
	QualitySummary         string `json:"quality_summary,omitempty"`
	QualityChecked         *int64 `json:"quality_checked,omitempty"`
	ExitIP                 string `json:"exit_ip,omitempty"`
	ExitCountry            string `json:"exit_country,omitempty"`
	ExitCountryCode        string `json:"exit_country_code,omitempty"`
	ExitRegion             string `json:"exit_region,omitempty"`
}

type ManageSelection struct {
	Mode         string      `json:"mode"`
	Names        []string    `json:"names,omitempty"`
	Filter       ManageQuery `json:"filter,omitempty"`
	ExcludeNames []string    `json:"exclude_names,omitempty"`
}

type ManageFacets struct {
	Regions             []string `json:"regions"`
	Sources             []string `json:"sources"`
	LifecycleStates     []string `json:"lifecycle_states"`
	ManualProbeStatuses []string `json:"manual_probe_statuses"`
	ActivationReadiness []string `json:"activation_readiness"`
	QualityStatuses     []string `json:"quality_statuses"`
}

type ManageListResponse struct {
	Items         []ManageRow    `json:"items"`
	Page          int            `json:"page"`
	PageSize      int            `json:"page_size"`
	Total         int            `json:"total"`
	FilteredTotal int            `json:"filtered_total"`
	Summary       map[string]int `json:"summary"`
	Facets        ManageFacets   `json:"facets"`
}

func BuildManageRows(configNodes []config.NodeConfig, snapshots []Snapshot) []ManageRow {
	snapshotsByURI := make(map[string]Snapshot, len(snapshots))
	snapshotsByName := make(map[string]Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		uriKey := normalizeLookupKey(snapshot.URI)
		if uriKey != "" {
			if _, exists := snapshotsByURI[uriKey]; !exists {
				snapshotsByURI[uriKey] = snapshot
			}
		}
		nameKey := normalizeLookupKey(snapshot.Name)
		if nameKey != "" {
			if _, exists := snapshotsByName[nameKey]; !exists {
				snapshotsByName[nameKey] = snapshot
			}
		}
	}

	rows := make([]ManageRow, 0, len(configNodes))
	for _, node := range configNodes {
		row := ManageRow{
			Name:                   node.Name,
			URI:                    node.URI,
			Port:                   node.Port,
			Username:               node.Username,
			Password:               node.Password,
			Source:                 string(node.Source),
			Disabled:               node.Disabled,
			LifecycleState:         normalizeManageLifecycleState(node.LifecycleState, node.Disabled),
			LatencyMS:              -1,
			ManualProbeStatus:      store.ManualProbeStatusUntested,
			ManualProbeLatencyMS:   -1,
			RuntimeStatus:          "pending",
			QualityVersion:         node.QualityVersion,
			QualityStatus:          node.QualityStatus,
			QualityOpenAIStatus:    node.QualityOpenAIStatus,
			QualityAnthropicStatus: node.QualityAnthropicStatus,
			QualityScore:           node.QualityScore,
			QualityGrade:           node.QualityGrade,
			QualitySummary:         node.QualitySummary,
			QualityChecked:         node.QualityChecked,
			ExitIP:                 node.ExitIP,
			ExitCountry:            node.ExitCountry,
			ExitCountryCode:        node.ExitCountryCode,
			ExitRegion:             node.ExitRegion,
		}
		applyManageQualityCompatibility(&row)
		applyManageActivationState(&row)

		if node.Disabled {
			row.RuntimeStatus = "disabled"
			rows = append(rows, row)
			continue
		}

		snapshot, ok := snapshotsByURI[normalizeLookupKey(node.URI)]
		if !ok {
			snapshot, ok = snapshotsByName[normalizeLookupKey(node.Name)]
		}
		if !ok {
			rows = append(rows, row)
			continue
		}

		row.Tag = snapshot.Tag
		row.LatencyMS = snapshot.LastLatencyMs
		row.Region = snapshot.Region
		row.Country = snapshot.Country
		row.ActiveConnections = int(snapshot.ActiveConnections)
		row.SuccessCount = snapshot.SuccessCount
		row.FailureCount = snapshot.FailureCount
		row.RuntimeStatus = resolveManageRuntimeStatus(snapshot)
		rows = append(rows, row)
	}

	return rows
}

func ApplyManageManualProbeResults(rows []ManageRow, storeNodes []store.Node, results map[int64]*store.NodeManualProbeResult) {
	if len(rows) == 0 || len(storeNodes) == 0 || len(results) == 0 {
		return
	}

	resultsByURI := make(map[string]*store.NodeManualProbeResult, len(results))
	resultsByName := make(map[string]*store.NodeManualProbeResult, len(results))
	for _, node := range storeNodes {
		result := results[node.ID]
		if result == nil {
			continue
		}

		if uriKey := normalizeLookupKey(node.URI); uriKey != "" {
			if _, exists := resultsByURI[uriKey]; !exists {
				resultsByURI[uriKey] = result
			}
		}
		if nameKey := normalizeLookupKey(node.Name); nameKey != "" {
			if _, exists := resultsByName[nameKey]; !exists {
				resultsByName[nameKey] = result
			}
		}
	}

	for idx := range rows {
		result, ok := resultsByURI[normalizeLookupKey(rows[idx].URI)]
		if !ok {
			result, ok = resultsByName[normalizeLookupKey(rows[idx].Name)]
		}
		if !ok {
			continue
		}
		applyManageManualProbeResult(&rows[idx], result)
	}
}

func QueryManageRows(rows []ManageRow, query ManageQuery) ManageListResponse {
	normalized := normalizeManageQuery(query)
	filtered := filterManageRows(rows, normalized)
	sortedRows := append([]ManageRow(nil), filtered...)
	sort.Slice(sortedRows, func(i, j int) bool {
		return compareManageRows(sortedRows[i], sortedRows[j], normalized.SortKey, normalized.SortDir) < 0
	})

	start := (normalized.Page - 1) * normalized.PageSize
	if start > len(sortedRows) {
		start = len(sortedRows)
	}
	end := start + normalized.PageSize
	if end > len(sortedRows) {
		end = len(sortedRows)
	}

	return ManageListResponse{
		Items:         sortedRows[start:end],
		Page:          normalized.Page,
		PageSize:      normalized.PageSize,
		Total:         len(rows),
		FilteredTotal: len(filtered),
		Summary:       buildManageSummary(rows),
		Facets:        buildManageFacets(rows),
	}
}

func ResolveManageSelection(rows []ManageRow, selection ManageSelection) ([]ManageRow, error) {
	switch strings.ToLower(strings.TrimSpace(selection.Mode)) {
	case "names":
		if len(selection.Names) == 0 {
			return nil, errInvalidManageSelection
		}
		nameSet := make(map[string]struct{}, len(selection.Names))
		for _, name := range selection.Names {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			nameSet[trimmed] = struct{}{}
		}
		if len(nameSet) == 0 {
			return nil, errInvalidManageSelection
		}
		selected := make([]ManageRow, 0, len(nameSet))
		for _, row := range rows {
			if _, ok := nameSet[row.Name]; ok {
				selected = append(selected, row)
			}
		}
		if len(selected) == 0 {
			return nil, errInvalidManageSelection
		}
		return selected, nil
	case "filter":
		filtered := filterManageRows(rows, normalizeManageSelectionFilter(selection.Filter))
		if len(filtered) == 0 {
			return nil, errInvalidManageSelection
		}
		excluded := make(map[string]struct{}, len(selection.ExcludeNames))
		for _, name := range selection.ExcludeNames {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			excluded[trimmed] = struct{}{}
		}
		selected := make([]ManageRow, 0, len(filtered))
		for _, row := range filtered {
			if _, skip := excluded[row.Name]; skip {
				continue
			}
			selected = append(selected, row)
		}
		if len(selected) == 0 {
			return nil, errInvalidManageSelection
		}
		return selected, nil
	default:
		return nil, errInvalidManageSelection
	}
}

func parseManageQuery(values url.Values) (ManageQuery, error) {
	query := ManageQuery{
		Page:              defaultManagePage,
		PageSize:          defaultManagePageSize,
		Keyword:           strings.TrimSpace(values.Get("keyword")),
		Status:            strings.TrimSpace(values.Get("status")),
		Region:            strings.TrimSpace(values.Get("region")),
		Source:            strings.TrimSpace(values.Get("source")),
		LifecycleState:    strings.TrimSpace(values.Get("lifecycle_state")),
		ManualProbeStatus: strings.TrimSpace(values.Get("manual_probe_status")),
		ActivationReady:   strings.TrimSpace(values.Get("activation_ready")),
		QualityStatus:     strings.TrimSpace(values.Get("quality_status")),
		SortKey:           ManageSortKey(strings.TrimSpace(values.Get("sort_key"))),
		SortDir:           strings.TrimSpace(values.Get("sort_dir")),
	}

	if page := strings.TrimSpace(values.Get("page")); page != "" {
		parsed, err := strconv.Atoi(page)
		if err != nil || parsed <= 0 {
			return ManageQuery{}, errors.New("invalid page")
		}
		query.Page = parsed
	}

	if pageSize := strings.TrimSpace(values.Get("page_size")); pageSize != "" {
		parsed, err := strconv.Atoi(pageSize)
		if err != nil || parsed <= 0 {
			return ManageQuery{}, errors.New("invalid page_size")
		}
		query.PageSize = parsed
	}

	return normalizeManageQuery(query), nil
}

func normalizeManageQuery(query ManageQuery) ManageQuery {
	normalized := query
	if normalized.Page <= 0 {
		normalized.Page = defaultManagePage
	}
	if normalized.PageSize <= 0 {
		normalized.PageSize = defaultManagePageSize
	}
	if normalized.PageSize > maxManagePageSize {
		normalized.PageSize = maxManagePageSize
	}
	normalized.Keyword = strings.TrimSpace(normalized.Keyword)
	normalized.Status = strings.TrimSpace(normalized.Status)
	normalized.Region = strings.TrimSpace(normalized.Region)
	normalized.Source = strings.TrimSpace(normalized.Source)
	normalized.LifecycleState = strings.TrimSpace(normalized.LifecycleState)
	normalized.ManualProbeStatus = strings.TrimSpace(normalized.ManualProbeStatus)
	if normalized.ManualProbeStatus != "" {
		normalized.ManualProbeStatus = normalizeManualProbeStatus(normalized.ManualProbeStatus)
	}
	normalized.ActivationReady = normalizeManageActivationReadyFilter(normalized.ActivationReady)
	normalized.QualityStatus = strings.TrimSpace(normalized.QualityStatus)
	if !isValidManageSortKey(normalized.SortKey) {
		normalized.SortKey = ManageSortByName
	}
	if normalized.SortDir != "desc" {
		normalized.SortDir = "asc"
	}
	return normalized
}

func normalizeManageSelectionFilter(query ManageQuery) ManageQuery {
	normalized := normalizeManageQuery(query)
	normalized.Page = defaultManagePage
	normalized.PageSize = maxManagePageSize
	normalized.SortKey = ManageSortByName
	normalized.SortDir = "asc"
	return normalized
}

func filterManageRows(rows []ManageRow, query ManageQuery) []ManageRow {
	filtered := make([]ManageRow, 0, len(rows))
	keyword := strings.ToLower(query.Keyword)

	for _, row := range rows {
		if keyword != "" {
			if !strings.Contains(strings.ToLower(row.Name), keyword) &&
				!strings.Contains(strings.ToLower(row.URI), keyword) &&
				!strings.Contains(strings.ToLower(row.Country), keyword) &&
				!strings.Contains(strings.ToLower(row.Region), keyword) {
				continue
			}
		}
		if query.Status != "" && row.RuntimeStatus != query.Status {
			continue
		}
		if query.Region != "" && row.Region != query.Region {
			continue
		}
		if query.Source != "" && row.Source != query.Source {
			continue
		}
		if query.LifecycleState != "" && row.LifecycleState != query.LifecycleState {
			continue
		}
		if query.ManualProbeStatus != "" && row.ManualProbeStatus != query.ManualProbeStatus {
			continue
		}
		if query.ActivationReady != "" && activationReadyFacetValue(row.ActivationReady) != query.ActivationReady {
			continue
		}
		if query.QualityStatus != "" && row.QualityStatus != query.QualityStatus {
			continue
		}
		filtered = append(filtered, row)
	}

	return filtered
}

func buildManageSummary(rows []ManageRow) map[string]int {
	summary := map[string]int{
		"normal":      0,
		"pending":     0,
		"unavailable": 0,
		"blacklisted": 0,
		"disabled":    0,
	}
	for _, row := range rows {
		summary[row.RuntimeStatus]++
	}
	return summary
}

func buildManageFacets(rows []ManageRow) ManageFacets {
	regionSet := make(map[string]struct{})
	sourceSet := make(map[string]struct{})
	lifecycleStateSet := make(map[string]struct{})
	manualProbeStatusSet := make(map[string]struct{})
	activationReadySet := make(map[string]struct{})
	qualityStatusSet := make(map[string]struct{})

	for _, row := range rows {
		if row.Region != "" {
			regionSet[row.Region] = struct{}{}
		}
		if row.Source != "" {
			sourceSet[row.Source] = struct{}{}
		}
		if row.LifecycleState != "" {
			lifecycleStateSet[row.LifecycleState] = struct{}{}
		}
		if row.ManualProbeStatus != "" {
			manualProbeStatusSet[row.ManualProbeStatus] = struct{}{}
		}
		activationReadySet[activationReadyFacetValue(row.ActivationReady)] = struct{}{}
		if row.QualityStatus != "" {
			qualityStatusSet[row.QualityStatus] = struct{}{}
		}
	}

	regions := make([]string, 0, len(regionSet))
	for region := range regionSet {
		regions = append(regions, region)
	}
	sources := make([]string, 0, len(sourceSet))
	for source := range sourceSet {
		sources = append(sources, source)
	}
	lifecycleStates := make([]string, 0, len(lifecycleStateSet))
	for lifecycleState := range lifecycleStateSet {
		lifecycleStates = append(lifecycleStates, lifecycleState)
	}
	manualProbeStatuses := make([]string, 0, len(manualProbeStatusSet))
	for manualProbeStatus := range manualProbeStatusSet {
		manualProbeStatuses = append(manualProbeStatuses, manualProbeStatus)
	}
	activationReadiness := make([]string, 0, len(activationReadySet))
	for readiness := range activationReadySet {
		activationReadiness = append(activationReadiness, readiness)
	}
	qualityStatuses := make([]string, 0, len(qualityStatusSet))
	for status := range qualityStatusSet {
		qualityStatuses = append(qualityStatuses, status)
	}

	slices.Sort(regions)
	slices.Sort(sources)
	slices.Sort(lifecycleStates)
	slices.Sort(manualProbeStatuses)
	slices.Sort(activationReadiness)
	slices.Sort(qualityStatuses)

	return ManageFacets{
		Regions:             regions,
		Sources:             sources,
		LifecycleStates:     lifecycleStates,
		ManualProbeStatuses: manualProbeStatuses,
		ActivationReadiness: activationReadiness,
		QualityStatuses:     qualityStatuses,
	}
}

func compareManageRows(a, b ManageRow, key ManageSortKey, dir string) int {
	var cmp int
	switch key {
	case ManageSortByStatus:
		cmp = compareInts(statusOrder(a.RuntimeStatus), statusOrder(b.RuntimeStatus))
	case ManageSortByLatency:
		cmp = compareInt64s(sortableLatency(a.LatencyMS), sortableLatency(b.LatencyMS))
	case ManageSortByRegion:
		cmp = strings.Compare(strings.ToLower(firstNonEmpty(a.Region, a.Country)), strings.ToLower(firstNonEmpty(b.Region, b.Country)))
	case ManageSortByPort:
		cmp = compareInts(int(a.Port), int(b.Port))
	case ManageSortBySource:
		cmp = strings.Compare(strings.ToLower(a.Source), strings.ToLower(b.Source))
	default:
		cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	if cmp == 0 {
		cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	if dir == "desc" {
		return -cmp
	}
	return cmp
}

func resolveManageRuntimeStatus(snapshot Snapshot) string {
	if snapshot.Blacklisted {
		return "blacklisted"
	}
	if !snapshot.InitialCheckDone {
		return "pending"
	}
	if snapshot.Available {
		return "normal"
	}
	return "unavailable"
}

func normalizeLookupKey(value string) string {
	return strings.TrimSpace(value)
}

func isValidManageSortKey(key ManageSortKey) bool {
	switch key {
	case ManageSortByName, ManageSortByStatus, ManageSortByLatency, ManageSortByRegion, ManageSortByPort, ManageSortBySource:
		return true
	default:
		return false
	}
}

func sortableLatency(value int64) int64 {
	if value < 0 {
		return int64(^uint64(0) >> 1)
	}
	return value
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareInt64s(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func statusOrder(status string) int {
	switch status {
	case "normal":
		return 0
	case "pending":
		return 1
	case "unavailable":
		return 2
	case "blacklisted":
		return 3
	case "disabled":
		return 4
	default:
		return 5
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func applyManageQualityCompatibility(row *ManageRow) {
	if row == nil || row.QualityVersion != "" || !hasLegacyManageQualityResult(row) {
		return
	}

	row.QualityStatus = manageQualityStatusUnchecked
	row.QualityOpenAIStatus = ""
	row.QualityAnthropicStatus = ""
	row.QualityScore = nil
	row.QualityGrade = ""
	row.QualitySummary = ""
	row.QualityChecked = nil
	row.ExitIP = ""
	row.ExitCountry = ""
	row.ExitCountryCode = ""
	row.ExitRegion = ""
}

func applyManageManualProbeResult(row *ManageRow, result *store.NodeManualProbeResult) {
	if row == nil || result == nil {
		return
	}

	row.ManualProbeStatus = result.Status
	row.ManualProbeLatencyMS = result.LatencyMs
	row.ManualProbeMessage = result.Message
	if !result.CheckedAt.IsZero() {
		checked := result.CheckedAt.Unix()
		row.ManualProbeChecked = &checked
	}
	applyManageActivationState(row)
}

func hasLegacyManageQualityResult(row *ManageRow) bool {
	if row == nil {
		return false
	}
	return row.QualityStatus != "" ||
		row.QualityOpenAIStatus != "" ||
		row.QualityAnthropicStatus != "" ||
		row.QualityScore != nil ||
		row.QualityGrade != "" ||
		row.QualitySummary != "" ||
		row.QualityChecked != nil ||
		row.ExitIP != "" ||
		row.ExitCountry != "" ||
		row.ExitCountryCode != "" ||
		row.ExitRegion != ""
}

func applyManageActivationState(row *ManageRow) {
	if row == nil {
		return
	}

	row.ActivationReady, row.ActivationBlockReason = deriveActivationState(
		row.ManualProbeStatus,
		row.QualityOpenAIStatus,
		row.QualityAnthropicStatus,
	)
}

func normalizeManageActivationReadyFilter(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case activationReadyReady, "true", "1", "yes":
		return activationReadyReady
	case activationReadyBlocked, "false", "0", "no":
		return activationReadyBlocked
	default:
		return ""
	}
}

func normalizeManageLifecycleState(value string, disabled bool) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	if disabled {
		return store.NodeLifecycleDisabled
	}
	return store.NodeLifecycleActive
}
