package boxmgr

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"easy_proxies/internal/builder"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/outbound/pool"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type temporaryConfigNodeRuntime struct {
	instance         *box.Box
	outbound         adapter.Outbound
	probeDestination M.Socksaddr
	probeReady       bool
	skipCertVerify   bool
}

func (m *Manager) CreateConfigNodeRuntime(ctx context.Context, node config.NodeConfig) (monitor.ConfigNodeRuntime, error) {
	m.mu.RLock()
	cfg := m.copyConfigLocked()
	m.mu.RUnlock()
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	probeDestination, probeReady := currentProbeDestination(cfg)
	return newTemporaryConfigNodeRuntime(ctx, cfg, node, probeDestination, probeReady)
}

func newTemporaryConfigNodeRuntime(ctx context.Context, baseCfg *config.Config, node config.NodeConfig, probeDestination M.Socksaddr, probeReady bool) (*temporaryConfigNodeRuntime, error) {
	tempCfg := baseCfg.Clone()
	tempCfg.Nodes = []config.NodeConfig{node}
	tempCfg.Mode = "pool"
	tempCfg.GeoIP.Enabled = false

	opts, err := builder.Build(tempCfg)
	if err != nil {
		return nil, fmt.Errorf("build temporary node runtime: %w", err)
	}
	opts.Inbounds = nil
	opts.Log = &option.LogOptions{Disabled: true}

	inboundRegistry := include.InboundRegistry()
	outboundRegistry := include.OutboundRegistry()
	pool.Register(outboundRegistry)
	endpointRegistry := include.EndpointRegistry()
	dnsRegistry := include.DNSTransportRegistry()
	serviceRegistry := include.ServiceRegistry()

	boxCtx := box.Context(ctx, inboundRegistry, outboundRegistry, endpointRegistry, dnsRegistry, serviceRegistry)
	instance, err := box.New(box.Options{Context: boxCtx, Options: opts})
	if err != nil {
		return nil, fmt.Errorf("create temporary sing-box instance: %w", err)
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return nil, fmt.Errorf("start temporary sing-box instance: %w", err)
	}

	outbound, ok := instance.Outbound().Outbound(pool.Tag)
	if !ok {
		_ = instance.Close()
		return nil, fmt.Errorf("temporary outbound %q not found", pool.Tag)
	}

	return &temporaryConfigNodeRuntime{
		instance:         instance,
		outbound:         outbound,
		probeDestination: probeDestination,
		probeReady:       probeReady,
		skipCertVerify:   tempCfg.SkipCertVerify,
	}, nil
}

func (r *temporaryConfigNodeRuntime) Probe(ctx context.Context) (time.Duration, error) {
	if r == nil || r.outbound == nil {
		return 0, fmt.Errorf("temporary runtime is not initialized")
	}
	if !r.probeReady {
		return 0, fmt.Errorf("probe target not configured")
	}

	start := time.Now()
	conn, err := r.outbound.DialContext(ctx, N.NetworkTCP, r.probeDestination)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	if _, err := temporaryHTTPProbe(ctx, conn, r.probeDestination.AddrString()); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func (r *temporaryConfigNodeRuntime) HTTPRequest(ctx context.Context, method, rawURL string, headers map[string]string, maxBodyBytes int64) (*monitor.HTTPCheckResult, error) {
	if r == nil || r.outbound == nil {
		return nil, fmt.Errorf("temporary runtime is not initialized")
	}

	transport := &http.Transport{
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: r.skipCertVerify,
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return r.outbound.DialContext(ctx, network, M.ParseSocksaddrHostPort(host, parseRuntimeProbePort(port)))
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	start := time.Now()
	resp, err := client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if maxBodyBytes <= 0 {
		maxBodyBytes = 8 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBodyBytes {
		body = body[:maxBodyBytes]
	}

	headerMap := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header {
		if len(values) > 0 {
			headerMap[key] = values[0]
		}
	}

	return &monitor.HTTPCheckResult{
		StatusCode: resp.StatusCode,
		LatencyMs:  latencyMs,
		Headers:    headerMap,
		Body:       body,
	}, nil
}

func (r *temporaryConfigNodeRuntime) Close() error {
	if r == nil || r.instance == nil {
		return nil
	}
	return r.instance.Close()
}

func currentProbeDestination(cfg *config.Config) (M.Socksaddr, bool) {
	if cfg == nil {
		return M.Socksaddr{}, false
	}

	target := strings.TrimSpace(cfg.Management.ProbeTarget)
	if target == "" {
		return M.Socksaddr{}, false
	}

	if strings.HasPrefix(target, "https://") {
		target = strings.TrimPrefix(target, "https://")
	} else if strings.HasPrefix(target, "http://") {
		target = strings.TrimPrefix(target, "http://")
	}
	if idx := strings.Index(target, "/"); idx != -1 {
		target = target[:idx]
	}

	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host = target
		if strings.HasPrefix(cfg.Management.ProbeTarget, "https://") {
			port = "443"
		} else {
			port = "80"
		}
	}

	return M.ParseSocksaddrHostPort(host, parseRuntimeProbePort(port)), true
}

func parseRuntimeProbePort(value string) uint16 {
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		return 80
	}
	return uint16(port)
}

func temporaryHTTPProbe(ctx context.Context, conn net.Conn, host string) (time.Duration, error) {
	req := fmt.Sprintf("GET /generate_204 HTTP/1.1\r\nHost: %s\r\nConnection: close\r\nUser-Agent: Mozilla/5.0\r\n\r\n", host)

	stopCancelWatch := watchTemporaryProbeContext(ctx, conn)
	defer stopCancelWatch()

	_ = conn.SetDeadline(temporaryProbeDeadline(ctx, 10*time.Second))

	start := time.Now()
	if _, err := conn.Write([]byte(req)); err != nil {
		return 0, fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	if _, err := reader.ReadByte(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, fmt.Errorf("probe cancelled: %w", ctxErr)
		}
		return 0, fmt.Errorf("read response: %w", err)
	}

	return time.Since(start), nil
}

func temporaryProbeDeadline(ctx context.Context, fallback time.Duration) time.Time {
	deadline := time.Now().Add(fallback)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

func watchTemporaryProbeContext(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}
