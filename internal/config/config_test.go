package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestIsProxyURIRecognizesHTTPAndSOCKS5(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{name: "http", uri: "http://alice:secret@example.com:8080", want: true},
		{name: "socks5", uri: "socks5://alice:secret@example.com:1080", want: true},
		{name: "vmess", uri: "vmess://example", want: true},
		{name: "invalid", uri: "ftp://example.com", want: false},
	}

	for _, tt := range tests {
		if got := IsProxyURI(tt.uri); got != tt.want {
			t.Fatalf("%s: IsProxyURI(%q) = %v, want %v", tt.name, tt.uri, got, tt.want)
		}
	}
}

func TestLoadAppliesExampleAlignedDefaults(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	if err := os.WriteFile(configPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.MultiPort.BasePort, uint16(24000); got != want {
		t.Fatalf("MultiPort.BasePort = %d, want %d", got, want)
	}

	if got, want := cfg.Management.Listen, "0.0.0.0:9888"; got != want {
		t.Fatalf("Management.Listen = %q, want %q", got, want)
	}

	if got, want := cfg.Management.ProbeTarget, "http://cp.cloudflare.com/generate_204"; got != want {
		t.Fatalf("Management.ProbeTarget = %q, want %q", got, want)
	}
}

func TestLoadDoesNotFetchLegacyOrTXTSubscriptionsOnInitialStartup(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("1.0.171.213:8080\n"))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := "subscriptions:\n" +
		"  - " + server.URL + "/legacy.txt\n" +
		"txt_subscriptions:\n" +
		"  - name: vmheaven\n" +
		"    url: " + server.URL + "/all_proxies.txt\n" +
		"    default_protocol: socks5\n" +
		"    auto_update_enabled: true\n"

	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.Nodes) != 0 {
		t.Fatalf("len(cfg.Nodes) = %d, want 0", len(cfg.Nodes))
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("subscription requests = %d, want 0", got)
	}
}
