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
	manualRefresh chan struct{}
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
		manualRefresh: make(chan struct{}, 1),
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
	hasSubscriptions := len(m.baseCfg.Subscriptions) > 0
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	healthCheckTimeout := m.baseCfg.SubscriptionRefresh.HealthCheckTimeout
	m.mu.RUnlock()

	if !hasSubscriptions {
		return fmt.Errorf("没有配置订阅链接")
	}

	select {
	case m.manualRefresh <- struct{}{}:
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
	status.Enabled = m.baseCfg.SubscriptionRefresh.Enabled
	status.HasSubscriptions = len(m.baseCfg.Subscriptions) > 0
	m.mu.RUnlock()

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
				m.doRefresh()
			}

			m.mu.Lock()
			if m.isEnabledLocked() {
				m.status.NextRefresh = time.Now().Add(interval)
			} else {
				m.status.NextRefresh = time.Time{}
			}
			m.mu.Unlock()

		case <-m.manualRefresh:
			// Manual refresh always executes (caller already verified subscriptions exist)
			m.doRefresh()
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
	return m.baseCfg.SubscriptionRefresh.Enabled && len(m.baseCfg.Subscriptions) > 0
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
// It fetches subscription nodes and persists them to the SQLite Store,
// then reloads the config. Manual nodes are preserved in the Store.
func (m *Manager) doRefresh() {
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

	// Fetch nodes from all subscriptions
	nodes, err := m.fetchAllSubscriptions()
	if err != nil {
		m.logger.Errorf("fetch subscriptions failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	if len(nodes) == 0 {
		m.logger.Warnf("no nodes fetched from subscriptions")
		m.mu.Lock()
		m.status.LastError = "no nodes fetched"
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.logger.Infof("fetched %d nodes from subscriptions", len(nodes))

	// Persist subscription nodes to Store (replace old subscription nodes)
	if m.store != nil {
		// Delete old subscription nodes
		deleted, err := m.store.DeleteNodesBySource(m.ctx, store.NodeSourceSubscription)
		if err != nil {
			m.logger.Warnf("failed to delete old subscription nodes from store: %v", err)
		} else if deleted > 0 {
			m.logger.Infof("deleted %d old subscription nodes from store", deleted)
		}

		// Insert new subscription nodes
		var storeNodes []store.Node
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
				URI:     uri,
				Name:    name,
				Source:  store.NodeSourceSubscription,
				Enabled: true,
			})
		}
		if err := m.store.BulkUpsertNodes(m.ctx, storeNodes); err != nil {
			m.logger.Errorf("failed to save subscription nodes to store: %v", err)
			m.mu.Lock()
			m.status.LastError = fmt.Sprintf("save to store: %v", err)
			m.status.LastRefresh = time.Now()
			m.mu.Unlock()
			return
		}
		m.logger.Infof("saved %d subscription nodes to store", len(storeNodes))
	} else {
		m.logger.Errorf("store is not available, cannot persist subscription nodes")
		m.mu.Lock()
		m.status.LastError = "store not available"
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	// Get current port mapping to preserve existing node ports
	portMap := m.boxMgr.CurrentPortMap()

	// Create new config with updated subscription nodes + preserved manual nodes
	newCfg := m.createNewConfig(nodes)

	// Trigger BoxManager reload with port preservation
	if err := m.boxMgr.ReloadWithPortMap(newCfg, portMap); err != nil {
		m.logger.Errorf("reload failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	// Count total nodes including manual
	totalNodes := len(newCfg.Nodes)
	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.NodeCount = totalNodes
	m.status.LastError = ""
	m.mu.Unlock()

	m.logger.Infof("subscription refresh completed, %d total nodes active (%d from subscription)", totalNodes, len(nodes))
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
