package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

const (
	qualityRequestTimeout = 15 * time.Second
	qualityMaxBodyBytes   = int64(8 * 1024)
	qualityUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
)

type qualityTarget struct {
	Name     string
	URL      string
	Method   string
	Statuses map[int]string
}

var qualityTargets = []qualityTarget{
	{
		Name:   "openai",
		URL:    "https://api.openai.com/v1/models",
		Method: http.MethodGet,
		Statuses: map[int]string{
			http.StatusUnauthorized: quality.StatusWarn,
		},
	},
	{
		Name:   "anthropic",
		URL:    "https://api.anthropic.com/v1/messages",
		Method: http.MethodGet,
		Statuses: map[int]string{
			http.StatusBadRequest:       quality.StatusWarn,
			http.StatusUnauthorized:     quality.StatusWarn,
			http.StatusNotFound:         quality.StatusWarn,
			http.StatusMethodNotAllowed: quality.StatusWarn,
		},
	},
	{
		Name:   "gemini",
		URL:    "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
		Method: http.MethodGet,
		Statuses: map[int]string{
			http.StatusOK:               quality.StatusPass,
			http.StatusUnauthorized:     quality.StatusWarn,
			http.StatusForbidden:        quality.StatusWarn,
			http.StatusTooManyRequests:  quality.StatusWarn,
		},
	},
}

type QualityChecker struct {
	monitorMgr *Manager
	store      store.Store
}

func NewQualityChecker(mgr *Manager, st store.Store) *QualityChecker {
	return &QualityChecker{monitorMgr: mgr, store: st}
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

	result := &quality.CheckResult{
		CheckedAt: time.Now(),
		Items:     make([]quality.CheckItem, 0, len(qualityTargets)+1),
	}

	baseItem := c.runBaseConnectivity(ctx, tag, result)
	result.Items = append(result.Items, baseItem)
	if baseItem.Status != quality.StatusPass {
		quality.FinalizeResult(result)
		check := toStoreQualityCheck(node.ID, result)
		if err := c.store.SaveNodeQualityCheck(ctx, check); err != nil {
			return nil, err
		}
		return check, nil
	}

	for _, target := range qualityTargets {
		result.Items = append(result.Items, c.runTarget(ctx, tag, target))
	}

	quality.FinalizeResult(result)
	check := toStoreQualityCheck(node.ID, result)
	if err := c.store.SaveNodeQualityCheck(ctx, check); err != nil {
		return nil, err
	}
	return check, nil
}

func (c *QualityChecker) runBaseConnectivity(ctx context.Context, tag string, result *quality.CheckResult) quality.CheckItem {
	reqCtx, cancel := context.WithTimeout(ctx, qualityRequestTimeout)
	defer cancel()

	headers := map[string]string{
		"Accept":     "application/json,text/plain,*/*",
		"User-Agent": qualityUserAgent,
	}

	type exitResponse struct {
		IP          string `json:"query"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		Region      string `json:"regionName"`
		Status      string `json:"status"`
	}

	resp, err := c.monitorMgr.HTTPRequest(reqCtx, tag, http.MethodGet, "http://ip-api.com/json/?lang=en", headers, qualityMaxBodyBytes)
	if err != nil {
		return quality.CheckItem{Target: "base_connectivity", Status: quality.StatusFail, Message: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return quality.CheckItem{Target: "base_connectivity", Status: quality.StatusFail, HTTPStatus: resp.StatusCode, LatencyMs: resp.LatencyMs, Message: fmt.Sprintf("unexpected status %d", resp.StatusCode)}
	}

	var exit exitResponse
	if err := json.Unmarshal(resp.Body, &exit); err != nil || strings.ToLower(exit.Status) != "success" {
		return quality.CheckItem{Target: "base_connectivity", Status: quality.StatusFail, HTTPStatus: resp.StatusCode, LatencyMs: resp.LatencyMs, Message: "failed to parse exit info"}
	}

	result.BaseLatencyMs = resp.LatencyMs
	result.ExitIP = exit.IP
	result.Country = exit.Country
	result.CountryCode = exit.CountryCode
	result.Region = exit.Region
	return quality.CheckItem{Target: "base_connectivity", Status: quality.StatusPass, HTTPStatus: resp.StatusCode, LatencyMs: resp.LatencyMs, Message: "proxy exit reachable"}
}

func (c *QualityChecker) runTarget(ctx context.Context, tag string, target qualityTarget) quality.CheckItem {
	reqCtx, cancel := context.WithTimeout(ctx, qualityRequestTimeout)
	defer cancel()

	headers := map[string]string{
		"Accept":     "application/json,text/html,*/*",
		"User-Agent": qualityUserAgent,
	}

	resp, err := c.monitorMgr.HTTPRequest(reqCtx, tag, target.Method, target.URL, headers, qualityMaxBodyBytes)
	if err != nil {
		return quality.CheckItem{Target: target.Name, Status: quality.StatusFail, Message: err.Error()}
	}

	status, ok := target.Statuses[resp.StatusCode]
	if ok {
		message := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if status == quality.StatusWarn {
			message = fmt.Sprintf("HTTP %d but target reachable", resp.StatusCode)
		}
		return quality.CheckItem{
			Target:     target.Name,
			Status:     status,
			HTTPStatus: resp.StatusCode,
			LatencyMs:  resp.LatencyMs,
			Message:    message,
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
		NodeID:           nodeID,
		QualityStatus:    result.Status,
		QualityScore:     &score,
		QualityGrade:     result.Grade,
		QualitySummary:   result.Summary,
		QualityCheckedAt: result.CheckedAt,
		ExitIP:           result.ExitIP,
		ExitCountry:      result.Country,
		ExitCountryCode:  result.CountryCode,
		ExitRegion:       result.Region,
		Items:            items,
	}
}
