package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

const (
	qualityRequestTimeout = 15 * time.Second
	qualityMaxBodyBytes   = int64(8 * 1024)
	qualityUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
)

type qualityTarget struct {
	Name         string
	URL          string
	Method       string
	PassStatuses map[int]struct{}
}

var qualityTargets = []qualityTarget{
	{
		Name:   quality.TargetOpenAIReachability,
		URL:    "https://api.openai.com/v1/models",
		Method: http.MethodGet,
		PassStatuses: map[int]struct{}{
			http.StatusUnauthorized: {},
			http.StatusForbidden:    {},
		},
	},
	{
		Name:   quality.TargetAnthropicReachability,
		URL:    "https://api.anthropic.com/v1/messages",
		Method: http.MethodGet,
		PassStatuses: map[int]struct{}{
			http.StatusUnauthorized:     {},
			http.StatusForbidden:        {},
			http.StatusMethodNotAllowed: {},
		},
	},
}

type QualityChecker struct {
	monitorMgr *Manager
	nodeMgr    NodeManager
	store      store.Store
}

func NewQualityChecker(mgr *Manager, st store.Store, nodeMgr NodeManager) *QualityChecker {
	return &QualityChecker{monitorMgr: mgr, nodeMgr: nodeMgr, store: st}
}

func (c *QualityChecker) CheckNode(ctx context.Context, tag string) (*store.NodeQualityCheck, error) {
	if c.monitorMgr == nil || c.store == nil {
		return nil, fmt.Errorf("quality checker dependencies are not initialized")
	}

	var snapshot *Snapshot
	for _, current := range c.monitorMgr.Snapshot() {
		if current.Tag == tag {
			copyValue := current
			snapshot = &copyValue
			break
		}
	}
	if snapshot == nil {
		return nil, fmt.Errorf("node %s not found", tag)
	}

	node, err := c.store.GetNodeByURI(ctx, snapshot.URI)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("store node for %s not found", snapshot.URI)
	}

	return c.runChecks(ctx, node.ID, func(reqCtx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error) {
		return c.monitorMgr.HTTPRequest(reqCtx, tag, method, rawURL, headers, maxBodyBytes)
	})
}

func (c *QualityChecker) CheckTarget(ctx context.Context, target BatchQualityTarget) (*store.NodeQualityCheck, error) {
	if strings.TrimSpace(target.Tag) != "" {
		return c.CheckNode(ctx, target.Tag)
	}
	return c.CheckConfigNode(ctx, target.ConfigNode, target.StoreNodeID)
}

func (c *QualityChecker) CheckConfigNode(ctx context.Context, node config.NodeConfig, storeNodeID int64) (*store.NodeQualityCheck, error) {
	if c.nodeMgr == nil || c.store == nil {
		return nil, fmt.Errorf("quality checker dependencies are not initialized")
	}
	if storeNodeID <= 0 {
		return nil, fmt.Errorf("store node id is required for config-backed quality checks")
	}

	runtimeNode, err := c.nodeMgr.CreateConfigNodeRuntime(ctx, node)
	if err != nil {
		return nil, err
	}
	defer runtimeNode.Close()

	return c.runChecks(ctx, storeNodeID, runtimeNode.HTTPRequest)
}

func (c *QualityChecker) runChecks(ctx context.Context, nodeID int64, request func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error)) (*store.NodeQualityCheck, error) {
	result := &quality.CheckResult{
		CheckedAt: time.Now(),
		Items:     make([]quality.CheckItem, 0, len(qualityTargets)),
	}

	for _, target := range qualityTargets {
		result.Items = append(result.Items, c.runTarget(ctx, request, target))
	}

	quality.FinalizeResult(result)
	check := toStoreQualityCheck(nodeID, result)
	manualProbe, err := c.store.GetNodeManualProbeResult(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	applyActivationStateToQualityCheck(check, manualProbe)
	if err := c.store.SaveNodeQualityCheck(ctx, check); err != nil {
		return nil, err
	}
	return check, nil
}

func (c *QualityChecker) runTarget(ctx context.Context, request func(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*HTTPCheckResult, error), target qualityTarget) quality.CheckItem {
	reqCtx, cancel := context.WithTimeout(ctx, qualityRequestTimeout)
	defer cancel()

	headers := map[string]string{
		"Accept":     "application/json,text/html,*/*",
		"User-Agent": qualityUserAgent,
	}

	resp, err := request(reqCtx, target.Method, target.URL, headers, qualityMaxBodyBytes)
	if err != nil {
		return quality.CheckItem{Target: target.Name, Status: quality.StatusFail, Message: err.Error()}
	}

	if _, ok := target.PassStatuses[resp.StatusCode]; ok {
		if matchesOfficialAPISignature(target.Name, resp) {
			return quality.CheckItem{
				Target:     target.Name,
				Status:     quality.StatusPass,
				HTTPStatus: resp.StatusCode,
				LatencyMs:  resp.LatencyMs,
				Message:    fmt.Sprintf("HTTP %d reached official endpoint", resp.StatusCode),
			}
		}
		return quality.CheckItem{
			Target:     target.Name,
			Status:     quality.StatusFail,
			HTTPStatus: resp.StatusCode,
			LatencyMs:  resp.LatencyMs,
			Message:    fmt.Sprintf("status %d missing expected official API signature", resp.StatusCode),
		}
	}

	return quality.CheckItem{
		Target:     target.Name,
		Status:     quality.StatusFail,
		HTTPStatus: resp.StatusCode,
		LatencyMs:  resp.LatencyMs,
		Message:    fmt.Sprintf("unexpected status %d", resp.StatusCode),
	}
}

func matchesOfficialAPISignature(target string, resp *HTTPCheckResult) bool {
	if resp == nil {
		return false
	}

	headers := normalizeHeaders(resp.Headers)
	body := strings.TrimSpace(string(resp.Body))

	switch target {
	case quality.TargetOpenAIReachability:
		return matchesOpenAISignature(headers, body)
	case quality.TargetAnthropicReachability:
		return matchesAnthropicSignature(headers, body)
	default:
		return false
	}
}

func matchesOpenAISignature(headers map[string]string, body string) bool {
	return hasHeaderKeyPrefix(headers, "openai-")
}

func matchesAnthropicSignature(headers map[string]string, body string) bool {
	if hasHeaderKeyPrefix(headers, "anthropic-") || headers["request-id"] != "" {
		return true
	}
	if body == "" || !looksLikeJSONResponse(headers, body) {
		return false
	}

	payload, ok := parseQualityErrorEnvelope(body)
	if !ok || payload.Type != "error" || payload.Error == nil || payload.Error.Type == "" || payload.Error.Message == "" {
		return false
	}

	return payload.RequestID != ""
}

func looksLikeJSONResponse(headers map[string]string, body string) bool {
	if contentType := headers["content-type"]; contentType != "" && strings.Contains(contentType, "application/json") {
		return true
	}
	return strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[")
}

func normalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}

	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		normalized[strings.ToLower(strings.TrimSpace(key))] = strings.ToLower(strings.TrimSpace(value))
	}
	return normalized
}

type qualityErrorEnvelope struct {
	Type      string              `json:"type"`
	RequestID string              `json:"request_id"`
	Error     *qualityErrorDetail `json:"error"`
}

type qualityErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func parseQualityErrorEnvelope(body string) (qualityErrorEnvelope, bool) {
	var payload qualityErrorEnvelope
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return qualityErrorEnvelope{}, false
	}

	payload.Type = normalizeErrorValue(payload.Type)
	payload.RequestID = strings.TrimSpace(payload.RequestID)
	if payload.Error != nil {
		payload.Error.Type = normalizeErrorValue(payload.Error.Type)
		payload.Error.Message = normalizeErrorValue(payload.Error.Message)
		payload.Error.Code = normalizeErrorValue(payload.Error.Code)
	}

	return payload, true
}

func normalizeErrorValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func hasHeaderKeyPrefix(headers map[string]string, prefix string) bool {
	for key := range headers {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func toStoreQualityCheck(nodeID int64, result *quality.CheckResult) *store.NodeQualityCheck {
	score := result.Score
	items := make([]store.NodeQualityCheckItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, store.NodeQualityCheckItem{
			Target:     item.Target,
			Status:     item.Status,
			HTTPStatus: item.HTTPStatus,
			LatencyMs:  item.LatencyMs,
			Message:    item.Message,
		})
	}

	return &store.NodeQualityCheck{
		NodeID:                 nodeID,
		QualityStatus:          result.Status,
		QualityVersion:         result.QualityVersion,
		QualityOpenAIStatus:    result.OpenAIStatus,
		QualityAnthropicStatus: result.AnthropicStatus,
		QualityScore:           &score,
		QualityGrade:           result.Grade,
		QualitySummary:         result.Summary,
		QualityCheckedAt:       result.CheckedAt,
		Items:                  items,
	}
}
