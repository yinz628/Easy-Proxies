package subscription

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
	"easy_proxies/internal/txtsub"
)

// Logger defines logging interface.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Option configures the Manager.
type Option func(*Manager)

// WithLogger sets a custom logger.
func WithLogger(l Logger) Option {
	return func(m *Manager) { m.logger = l }
}

// WithStore sets the data store.
func WithStore(s store.Store) Option {
	return func(m *Manager) { m.store = s }
}

// Ensure Manager implements boxmgr.ConfigUpdateListener.
var _ boxmgr.ConfigUpdateListener = (*Manager)(nil)

type feedKind string

const (
	feedKindLegacy feedKind = "legacy"
	feedKindTXT    feedKind = "txt"
)

type refreshRequest struct {
	includeDisabledTXT bool
}

type feedRequest struct {
	Kind              feedKind
	Name              string
	URL               string
	NormalizedURL     string
	FeedKey           string
	DefaultProtocol   txtsub.DefaultProtocol
	AutoUpdateEnabled bool
}

type feedFetchResult struct {
	Nodes        []config.NodeConfig
	ValidNodes   int
	SkippedLines int
}

// Manager handles periodic subscription refresh.
type Manager struct {
	mu sync.RWMutex

	baseCfg    *config.Config
	boxMgr     *boxmgr.Manager
	logger     Logger
	httpClient *http.Client // Custom HTTP client with connection pooling
	store      store.Store  // Data store for persisting nodes

	status        monitor.SubscriptionStatus
	ctx           context.Context
	cancel        context.CancelFunc
	refreshMu     sync.Mutex // prevents concurrent refreshes
	manualRefresh chan refreshRequest
	configChanged chan struct{} // signals config updates to the refresh loop
}

// New creates a SubscriptionManager.
func New(cfg *config.Config, boxMgr *boxmgr.Manager, opts ...Option) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	// Create optimized HTTP client with connection pooling
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second, // Overall timeout
	}

	m := &Manager{
		baseCfg:       cfg,
		boxMgr:        boxMgr,
		ctx:           ctx,
		cancel:        cancel,
		manualRefresh: make(chan refreshRequest, 1),
		configChanged: make(chan struct{}, 1),
		httpClient:    httpClient,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = defaultLogger{}
	}
	return m
}

// Start begins the background goroutine that manages periodic subscription refresh.
// The goroutine dynamically checks config to decide whether to actually perform refreshes,
// so it's safe to call Start() even when subscription refresh is initially disabled.
func (m *Manager) Start() {
	if m.isEnabled() {
		m.logger.Infof("starting subscription refresh, interval: %s", m.currentInterval())
	} else {
		m.logger.Infof("subscription manager started (auto-refresh currently disabled, will activate on config change)")
	}

	go m.refreshLoop()
}

// Stop stops the periodic refresh.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	// Close idle connections
	if m.httpClient != nil {
		m.httpClient.CloseIdleConnections()
	}
}

// RefreshNow triggers an immediate refresh, regardless of whether auto-refresh is enabled.
// It only requires that subscription URLs are configured.
func (m *Manager) RefreshNow() error {
	m.mu.RLock()
	hasSubscriptions := m.hasAnySubscriptionsLocked()
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	healthCheckTimeout := m.baseCfg.SubscriptionRefresh.HealthCheckTimeout
	m.mu.RUnlock()

	if !hasSubscriptions {
		return fmt.Errorf("没有配置订阅链接")
	}

	select {
	case m.manualRefresh <- refreshRequest{includeDisabledTXT: true}:
	default:
		// Already a refresh pending
	}

	// Wait for refresh to complete or timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout+healthCheckTimeout)
	defer cancel()

	// Poll status until refresh completes
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	startCount := m.Status().RefreshCount
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("refresh timeout")
		case <-ticker.C:
			status := m.Status()
			if status.RefreshCount > startCount {
				if status.LastError != "" {
					return fmt.Errorf("refresh failed: %s", status.LastError)
				}
				return nil
			}
		}
	}
}

// Status returns the current refresh status, including dynamic config state.
func (m *Manager) Status() monitor.SubscriptionStatus {
	m.mu.RLock()
	status := m.status
	cfg := m.baseCfg.Clone()
	existingFeeds := make([]monitor.SubscriptionFeedStatus, len(status.Feeds))
	copy(existingFeeds, status.Feeds)
	m.mu.RUnlock()

	if cfg != nil {
		status.Enabled = cfg.SubscriptionRefresh.Enabled
		status.HasSubscriptions = len(cfg.Subscriptions) > 0 || len(cfg.TXTSubscriptions) > 0
		status.Feeds = buildConfiguredFeedStatuses(cfg, existingFeeds)
	}

	// Check if nodes have been modified since last refresh
	status.NodesModified = m.CheckNodesModified()
	return status
}

// refreshLoop runs the background loop that manages periodic and manual refreshes.
// It dynamically reads config to decide whether to auto-refresh and at what interval.
func (m *Manager) refreshLoop() {
	interval := m.currentInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Set initial next refresh time
	m.mu.Lock()
	if m.isEnabledLocked() {
		m.status.NextRefresh = time.Now().Add(interval)
	}
	m.mu.Unlock()

	for {
		select {
		case <-m.ctx.Done():
			return

		case <-ticker.C:
			// Dynamically adjust interval if config changed
			newInterval := m.currentInterval()
			if newInterval != interval {
				m.logger.Infof("subscription refresh interval changed: %s → %s", interval, newInterval)
				interval = newInterval
				ticker.Reset(interval)
			}

			// Only auto-refresh when enabled and subscriptions exist
			if m.isEnabled() {
				m.doRefresh(false)
			}

			m.mu.Lock()
			if m.isEnabledLocked() {
				m.status.NextRefresh = time.Now().Add(interval)
			} else {
				m.status.NextRefresh = time.Time{}
			}
			m.mu.Unlock()

		case req := <-m.manualRefresh:
			// Manual refresh always executes (caller already verified subscriptions exist)
			m.doRefresh(req.includeDisabledTXT)
			// Reset ticker and recalculate interval after manual refresh
			newInterval := m.currentInterval()
			if newInterval != interval {
				interval = newInterval
			}
			ticker.Reset(interval)
			m.mu.Lock()
			m.status.NextRefresh = time.Now().Add(interval)
			m.mu.Unlock()

		case <-m.configChanged:
			// Config was updated (e.g., after reload), recalculate interval
			newInterval := m.currentInterval()
			if newInterval != interval {
				m.logger.Infof("subscription refresh interval changed: %s → %s", interval, newInterval)
				interval = newInterval
				ticker.Reset(interval)
			}
			m.mu.Lock()
			if m.isEnabledLocked() {
				m.status.NextRefresh = time.Now().Add(interval)
			} else {
				m.status.NextRefresh = time.Time{}
			}
			m.mu.Unlock()
		}
	}
}

// isEnabled checks if auto-refresh should run (acquires read lock).
func (m *Manager) isEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isEnabledLocked()
}

// isEnabledLocked checks if auto-refresh should run (caller must hold mu).
func (m *Manager) isEnabledLocked() bool {
	return m.baseCfg.SubscriptionRefresh.Enabled && m.hasAnySubscriptionsLocked()
}

func (m *Manager) hasAnySubscriptionsLocked() bool {
	return len(m.baseCfg.Subscriptions) > 0 || len(m.baseCfg.TXTSubscriptions) > 0
}

func (m *Manager) collectFeedRequests(includeDisabledTXT bool) []feedRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var feeds []feedRequest
	for _, subURL := range m.baseCfg.Subscriptions {
		trimmed := strings.TrimSpace(subURL)
		if trimmed == "" {
			continue
		}
		feeds = append(feeds, feedRequest{
			Kind:          feedKindLegacy,
			URL:           trimmed,
			NormalizedURL: trimmed,
			FeedKey:       txtsub.BuildFeedKey(txtsub.FeedKindLegacy, trimmed),
		})
	}

	for _, txtCfg := range m.baseCfg.TXTSubscriptions {
		trimmedURL := strings.TrimSpace(txtCfg.URL)
		if trimmedURL == "" {
			continue
		}
		if !includeDisabledTXT && !txtCfg.AutoUpdateEnabled {
			continue
		}
		defaultProtocol, err := txtsub.NormalizeDefaultProtocol(txtCfg.DefaultProtocol)
		if err != nil {
			continue
		}
		normalizedURL := txtsub.NormalizeSourceURL(trimmedURL)
		feeds = append(feeds, feedRequest{
			Kind:              feedKindTXT,
			Name:              strings.TrimSpace(txtCfg.Name),
			URL:               trimmedURL,
			NormalizedURL:     normalizedURL,
			FeedKey:           txtsub.BuildFeedKey(txtsub.FeedKindTXT, normalizedURL),
			DefaultProtocol:   defaultProtocol,
			AutoUpdateEnabled: txtCfg.AutoUpdateEnabled,
		})
	}

	return feeds
}

func (m *Manager) findTXTFeedRequestByFeedKey(feedKey string) (feedRequest, error) {
	feeds := m.collectFeedRequests(true)
	for _, feed := range feeds {
		if feed.FeedKey != feedKey {
			continue
		}
		if feed.Kind != feedKindTXT {
			return feedRequest{}, fmt.Errorf("feed %q is not a txt subscription", feedKey)
		}
		return feed, nil
	}
	return feedRequest{}, fmt.Errorf("txt feed %q not found", feedKey)
}

func buildConfiguredFeedStatuses(cfg *config.Config, existing []monitor.SubscriptionFeedStatus) []monitor.SubscriptionFeedStatus {
	existingByKey := make(map[string]monitor.SubscriptionFeedStatus, len(existing))
	for _, feed := range existing {
		existingByKey[feed.FeedKey] = feed
	}

	statuses := make([]monitor.SubscriptionFeedStatus, 0, len(cfg.Subscriptions)+len(cfg.TXTSubscriptions))
	for _, subURL := range cfg.Subscriptions {
		trimmed := strings.TrimSpace(subURL)
		if trimmed == "" {
			continue
		}
		feedKey := txtsub.BuildFeedKey(txtsub.FeedKindLegacy, trimmed)
		status := existingByKey[feedKey]
		status.FeedKey = feedKey
		status.Name = trimmed
		status.Type = string(feedKindLegacy)
		status.URL = trimmed
		status.AutoUpdateEnabled = true
		statuses = append(statuses, status)
	}

	for _, txtCfg := range cfg.TXTSubscriptions {
		trimmed := strings.TrimSpace(txtCfg.URL)
		if trimmed == "" {
			continue
		}
		normalizedURL := txtsub.NormalizeSourceURL(trimmed)
		feedKey := txtsub.BuildFeedKey(txtsub.FeedKindTXT, normalizedURL)
		status := existingByKey[feedKey]
		status.FeedKey = feedKey
		status.Name = strings.TrimSpace(txtCfg.Name)
		if status.Name == "" {
			status.Name = trimmed
		}
		status.Type = string(feedKindTXT)
		status.URL = trimmed
		status.AutoUpdateEnabled = txtCfg.AutoUpdateEnabled
		statuses = append(statuses, status)
	}

	return statuses
}

// currentInterval returns the configured refresh interval (acquires read lock).
func (m *Manager) currentInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentIntervalLocked()
}

// currentIntervalLocked returns the configured refresh interval (caller must hold mu).
func (m *Manager) currentIntervalLocked() time.Duration {
	interval := m.baseCfg.SubscriptionRefresh.Interval
	if interval <= 0 {
		interval = 1 * time.Hour
	}
	return interval
}

// doRefresh performs a single refresh operation.
// It fetches configured feeds, updates their nodes in the SQLite Store,
// then triggers a runtime reload.
func (m *Manager) doRefresh(includeDisabledTXT bool) {
	// Prevent concurrent refreshes
	if !m.refreshMu.TryLock() {
		m.logger.Warnf("refresh already in progress, skipping")
		return
	}
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	m.status.IsRefreshing = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.status.IsRefreshing = false
		m.status.RefreshCount++
		m.mu.Unlock()
	}()

	m.logger.Infof("starting subscription refresh")

	m.mu.RLock()
	cfgSnapshot := m.baseCfg.Clone()
	previousFeedStatuses := make([]monitor.SubscriptionFeedStatus, len(m.status.Feeds))
	copy(previousFeedStatuses, m.status.Feeds)
	m.mu.RUnlock()

	feeds := m.collectFeedRequests(includeDisabledTXT)
	feedStatuses := buildConfiguredFeedStatuses(cfgSnapshot, previousFeedStatuses)
	feedStatusIndex := make(map[string]int, len(feedStatuses))
	for idx := range feedStatuses {
		feedStatusIndex[feedStatuses[idx].FeedKey] = idx
	}
	if len(feeds) == 0 {
		m.mu.Lock()
		m.status.LastError = "no subscriptions configured"
		m.status.LastRefresh = time.Now()
		m.status.Feeds = feedStatuses
		m.mu.Unlock()
		return
	}

	if m.store == nil {
		m.mu.Lock()
		m.status.LastError = "store not available"
		m.status.LastRefresh = time.Now()
		m.status.Feeds = feedStatuses
		m.mu.Unlock()
		return
	}

	updatedFeeds := 0
	var lastErr string

	for _, feed := range feeds {
		fetched, err := m.fetchFeedNodes(feed)
		statusIdx, hasStatus := feedStatusIndex[feed.FeedKey]
		if err != nil {
			m.logger.Warnf("failed to refresh %s feed %q: %v", feed.Kind, feed.NormalizedURL, err)
			lastErr = err.Error()
			if hasStatus {
				feedStatuses[statusIdx].LastError = err.Error()
			}
			continue
		}
		if len(fetched.Nodes) == 0 {
			lastErr = fmt.Sprintf("feed %q returned no valid nodes", feed.NormalizedURL)
			if hasStatus {
				feedStatuses[statusIdx].LastError = lastErr
				feedStatuses[statusIdx].SkippedLines = fetched.SkippedLines
			}
			continue
		}

		storeNodes := buildStoreNodesForFeed(feed, fetched.Nodes)
		if err := m.persistFeedNodes(feed, storeNodes); err != nil {
			lastErr = fmt.Sprintf("save feed %s: %v", feed.FeedKey, err)
			if hasStatus {
				feedStatuses[statusIdx].LastError = lastErr
			}
			continue
		}
		if hasStatus {
			feedStatuses[statusIdx].LastRefresh = time.Now()
			feedStatuses[statusIdx].LastError = ""
			feedStatuses[statusIdx].ValidNodes = fetched.ValidNodes
			feedStatuses[statusIdx].SkippedLines = fetched.SkippedLines
		}
		updatedFeeds++
	}

	if updatedFeeds == 0 {
		m.mu.Lock()
		m.status.LastError = lastErr
		m.status.LastRefresh = time.Now()
		m.status.Feeds = feedStatuses
		m.mu.Unlock()
		return
	}

	if err := m.boxMgr.TriggerReload(m.ctx); err != nil {
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	enabled := true
	totalNodes, err := m.store.CountNodes(m.ctx, store.NodeFilter{Enabled: &enabled})
	if err != nil {
		m.logger.Warnf("failed to count nodes after refresh: %v", err)
	}
	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.NodeCount = int(totalNodes)
	m.status.LastError = ""
	m.status.Feeds = feedStatuses
	m.mu.Unlock()

	m.logger.Infof("subscription refresh completed, %d feeds updated, %d total nodes active", updatedFeeds, totalNodes)
}

// OnConfigUpdate is called by boxmgr after a successful reload.
// It updates the subscription manager's reference to the latest config
// so that subsequent refreshes use updated subscription URLs and settings.
func (m *Manager) OnConfigUpdate(cfg *config.Config) {
	if cfg == nil {
		return
	}
	m.mu.Lock()
	m.baseCfg = cfg
	m.mu.Unlock()
	m.logger.Infof("subscription manager config updated after reload")

	// Notify the refresh loop about config changes so it can
	// recalculate interval and enable/disable auto-refresh dynamically.
	select {
	case m.configChanged <- struct{}{}:
	default:
	}
}

// CheckNodesModified always returns false — with SQLite Store,
// node modifications are tracked in the database, not via file hashes.
func (m *Manager) CheckNodesModified() bool {
	return false
}

// MarkNodesModified updates the modification status.
func (m *Manager) MarkNodesModified() {
	m.mu.Lock()
	m.status.NodesModified = true
	m.mu.Unlock()
}

// fetchAllSubscriptions fetches nodes from all configured subscription URLs.
func (m *Manager) fetchAllSubscriptions() ([]config.NodeConfig, error) {
	var allNodes []config.NodeConfig
	var lastErr error

	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	for _, subURL := range m.baseCfg.Subscriptions {
		nodes, err := m.fetchSubscription(subURL, timeout)
		if err != nil {
			m.logger.Warnf("failed to fetch %s: %v", subURL, err)
			lastErr = err
			continue
		}
		m.logger.Infof("fetched %d nodes from subscription", len(nodes))
		allNodes = append(allNodes, nodes...)
	}

	if len(allNodes) == 0 && lastErr != nil {
		return nil, lastErr
	}

	return allNodes, nil
}

func (m *Manager) fetchFeedNodes(feed feedRequest) (feedFetchResult, error) {
	switch feed.Kind {
	case feedKindLegacy:
		nodes, err := m.fetchSubscription(feed.URL, m.baseCfg.SubscriptionRefresh.Timeout)
		if err != nil {
			return feedFetchResult{}, err
		}
		for idx := range nodes {
			nodes[idx].Source = config.NodeSourceSubscription
			nodes[idx].FeedKey = feed.FeedKey
		}
		return feedFetchResult{Nodes: nodes, ValidNodes: len(nodes)}, nil
	case feedKindTXT:
		return m.fetchTXTSubscription(feed)
	default:
		return feedFetchResult{}, fmt.Errorf("unsupported feed kind %q", feed.Kind)
	}
}

func (m *Manager) RefreshFeed(feedKey string) error {
	feed, err := m.findTXTFeedRequestByFeedKey(strings.TrimSpace(feedKey))
	if err != nil {
		return err
	}

	if !m.refreshMu.TryLock() {
		return fmt.Errorf("refresh already in progress")
	}
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	m.status.IsRefreshing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.status.IsRefreshing = false
		m.status.RefreshCount++
		m.mu.Unlock()
	}()

	fetched, err := m.fetchFeedNodes(feed)
	if err != nil {
		return err
	}
	if len(fetched.Nodes) == 0 {
		return fmt.Errorf("feed %q returned no valid nodes", feed.NormalizedURL)
	}

	if m.store == nil {
		return fmt.Errorf("store not available")
	}

	storeNodes := buildStoreNodesForFeed(feed, fetched.Nodes)
	if err := m.persistFeedNodes(feed, storeNodes); err != nil {
		return err
	}

	if err := m.boxMgr.TriggerReload(m.ctx); err != nil {
		return err
	}

	status := m.Status()
	for idx := range status.Feeds {
		if status.Feeds[idx].FeedKey == feed.FeedKey {
			status.Feeds[idx].LastRefresh = time.Now()
			status.Feeds[idx].LastError = ""
			status.Feeds[idx].ValidNodes = fetched.ValidNodes
			status.Feeds[idx].SkippedLines = fetched.SkippedLines
		}
	}

	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.LastError = ""
	m.status.Feeds = status.Feeds
	m.mu.Unlock()

	return nil
}

// fetchSubscription fetches and parses a single subscription URL.
func (m *Manager) fetchSubscription(subURL string, timeout time.Duration) ([]config.NodeConfig, error) {
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")

	// Use custom HTTP client with connection pooling
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Limit read size to prevent memory exhaustion
	const maxBodySize = 10 * 1024 * 1024 // 10MB
	limitedReader := io.LimitReader(resp.Body, maxBodySize)

	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseSubscriptionContent(string(body))
}

func (m *Manager) fetchTXTSubscription(feed feedRequest) (feedFetchResult, error) {
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", feed.NormalizedURL, nil)
	if err != nil {
		return feedFetchResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return feedFetchResult{}, fmt.Errorf("fetch txt subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return feedFetchResult{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	const maxBodySize = 10 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return feedFetchResult{}, fmt.Errorf("read body: %w", err)
	}

	result, err := txtsub.ParseContent(string(body), feed.Name, feed.DefaultProtocol)
	if err != nil {
		return feedFetchResult{}, err
	}
	nodes := make([]config.NodeConfig, 0, len(result.Entries))
	for _, entry := range result.Entries {
		nodes = append(nodes, config.NodeConfig{
			Name:    entry.Name,
			URI:     entry.URI,
			Source:  config.NodeSourceTXTSubscription,
			FeedKey: feed.FeedKey,
		})
	}
	return feedFetchResult{
		Nodes:        nodes,
		ValidNodes:   len(nodes),
		SkippedLines: result.SkippedLines,
	}, nil
}

func (m *Manager) persistFeedNodes(feed feedRequest, nodes []store.Node) error {
	switch feed.Kind {
	case feedKindTXT:
		return m.store.ReplaceTXTFeedNodes(m.ctx, feed.FeedKey, nodes)
	default:
		if _, err := m.store.DeleteNodesByFeedKey(m.ctx, feed.FeedKey); err != nil {
			return err
		}
		return m.store.BulkUpsertNodes(m.ctx, nodes)
	}
}

func buildStoreNodesForFeed(feed feedRequest, nodes []config.NodeConfig) []store.Node {
	storeNodes := make([]store.Node, 0, len(nodes))
	for _, n := range nodes {
		name := strings.TrimSpace(n.Name)
		uri := strings.TrimSpace(n.URI)
		if name == "" {
			if parsed, parseErr := url.Parse(uri); parseErr == nil && parsed.Fragment != "" {
				if decoded, decErr := url.QueryUnescape(parsed.Fragment); decErr == nil {
					name = decoded
				} else {
					name = parsed.Fragment
				}
			}
		}
		if name == "" {
			name = fmt.Sprintf("sub-node-%d", len(storeNodes)+1)
		}
		storeNodes = append(storeNodes, store.Node{
			URI:      uri,
			Name:     name,
			Source:   string(n.Source),
			FeedKey:  feed.FeedKey,
			Port:     n.Port,
			Username: n.Username,
			Password: n.Password,
			Enabled:  true,
		})
	}
	return storeNodes
}

// createNewConfig creates a new config with updated subscription nodes while
// preserving inline nodes (from config.yaml) and manual nodes (from Store).
func (m *Manager) createNewConfig(subNodes []config.NodeConfig) *config.Config {
	// Deep copy base config (uses Clone to avoid copying the mutex)
	m.mu.RLock()
	baseCfg := m.baseCfg
	m.mu.RUnlock()

	newCfg := baseCfg.Clone()

	// Start with inline nodes from config.yaml
	var allNodes []config.NodeConfig
	for _, node := range baseCfg.Nodes {
		if node.Source == config.NodeSourceInline {
			allNodes = append(allNodes, node)
		}
	}

	// Process subscription node names and mark source
	for i := range subNodes {
		subNodes[i].Name = strings.TrimSpace(subNodes[i].Name)
		subNodes[i].URI = strings.TrimSpace(subNodes[i].URI)
		subNodes[i].Source = config.NodeSourceSubscription

		// Extract name from URI fragment if not provided
		if subNodes[i].Name == "" {
			if parsed, err := url.Parse(subNodes[i].URI); err == nil && parsed.Fragment != "" {
				if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
					subNodes[i].Name = decoded
				} else {
					subNodes[i].Name = parsed.Fragment
				}
			}
		}
		if subNodes[i].Name == "" {
			subNodes[i].Name = fmt.Sprintf("node-%d", i)
		}
	}
	allNodes = append(allNodes, subNodes...)

	// Load manual nodes from Store
	if m.store != nil {
		storeManualNodes, err := m.store.ListNodes(m.ctx, store.NodeFilter{Source: store.NodeSourceManual})
		if err != nil {
			m.logger.Warnf("failed to load manual nodes from store: %v", err)
		} else if len(storeManualNodes) > 0 {
			for _, sn := range storeManualNodes {
				name := strings.TrimSpace(sn.Name)
				uri := strings.TrimSpace(sn.URI)
				if name == "" {
					if parsed, err := url.Parse(uri); err == nil && parsed.Fragment != "" {
						if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
							name = decoded
						} else {
							name = parsed.Fragment
						}
					}
				}
				if name == "" {
					name = fmt.Sprintf("manual-%d", sn.ID)
				}
				allNodes = append(allNodes, config.NodeConfig{
					Name:     name,
					URI:      uri,
					Port:     sn.Port,
					Username: sn.Username,
					Password: sn.Password,
					Source:   config.NodeSourceManual,
				})
			}
			m.logger.Infof("preserved %d manual nodes from store during subscription refresh", len(storeManualNodes))
		}
	}

	// Assign port numbers to all nodes in multi-port mode
	if newCfg.Mode == "multi-port" {
		portCursor := newCfg.MultiPort.BasePort
		for i := range allNodes {
			allNodes[i].Port = portCursor
			portCursor++
			// Apply default credentials
			if allNodes[i].Username == "" {
				allNodes[i].Username = newCfg.MultiPort.Username
				allNodes[i].Password = newCfg.MultiPort.Password
			}
		}
	}

	newCfg.Nodes = allNodes
	return newCfg
}

// parseSubscriptionContent parses subscription content in various formats.
// This is a simplified version - the full implementation is in config package.
func parseSubscriptionContent(content string) ([]config.NodeConfig, error) {
	content = strings.TrimSpace(content)

	// Check if it's base64 encoded
	if isBase64(content) {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				return parseNodesFromContent(content)
			}
		}
		content = string(decoded)
	}

	// Parse as plain text (one URI per line)
	return parseNodesFromContent(content)
}

func isBase64(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	if strings.Contains(s, "://") {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		_, err = base64.RawStdEncoding.DecodeString(s)
	}
	return err == nil
}

func parseNodesFromContent(content string) ([]config.NodeConfig, error) {
	var nodes []config.NodeConfig
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if config.IsProxyURI(line) {
			nodes = append(nodes, config.NodeConfig{URI: line})
		}
	}
	return nodes, nil
}

type defaultLogger struct{}

func (defaultLogger) Infof(format string, args ...any) {
	log.Printf("[subscription] "+format, args...)
}

func (defaultLogger) Warnf(format string, args ...any) {
	log.Printf("[subscription] WARN: "+format, args...)
}

func (defaultLogger) Errorf(format string, args ...any) {
	log.Printf("[subscription] ERROR: "+format, args...)
}
