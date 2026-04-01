package subscription

import (
	"testing"

	"easy_proxies/internal/config"
)

func TestCollectFeedRequestsIncludesOnlyEnabledTXTFeedsForAutoRefresh(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			Subscriptions: []string{"https://example.com/sub"},
			TXTSubscriptions: []config.TXTSubscriptionConfig{
				{
					Name:              "txt-enabled",
					URL:               "https://github.com/user/repo/blob/main/http.txt",
					DefaultProtocol:   "http",
					AutoUpdateEnabled: true,
				},
				{
					Name:              "txt-disabled",
					URL:               "https://github.com/user/repo/blob/main/socks.txt",
					DefaultProtocol:   "socks5",
					AutoUpdateEnabled: false,
				},
			},
		},
	}

	feeds := manager.collectFeedRequests(false)
	if len(feeds) != 2 {
		t.Fatalf("len(feeds) = %d, want 2", len(feeds))
	}

	if feeds[0].Kind != feedKindLegacy {
		t.Fatalf("feeds[0].Kind = %q, want %q", feeds[0].Kind, feedKindLegacy)
	}

	if feeds[1].Name != "txt-enabled" {
		t.Fatalf("feeds[1].Name = %q, want %q", feeds[1].Name, "txt-enabled")
	}
}

func TestCollectFeedRequestsIncludesDisabledTXTFeedsForManualRefresh(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			TXTSubscriptions: []config.TXTSubscriptionConfig{
				{
					Name:              "txt-disabled",
					URL:               "https://github.com/user/repo/blob/main/http.txt",
					DefaultProtocol:   "http",
					AutoUpdateEnabled: false,
				},
			},
		},
	}

	feeds := manager.collectFeedRequests(true)
	if len(feeds) != 1 {
		t.Fatalf("len(feeds) = %d, want 1", len(feeds))
	}

	if feeds[0].Kind != feedKindTXT {
		t.Fatalf("feeds[0].Kind = %q, want %q", feeds[0].Kind, feedKindTXT)
	}

	if feeds[0].NormalizedURL != "https://raw.githubusercontent.com/user/repo/main/http.txt" {
		t.Fatalf("feeds[0].NormalizedURL = %q, want GitHub raw URL", feeds[0].NormalizedURL)
	}
}

func TestFindTXTFeedRequestByFeedKeyReturnsOnlyMatchingTXTFeed(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			Subscriptions: []string{"https://example.com/sub"},
			TXTSubscriptions: []config.TXTSubscriptionConfig{
				{
					Name:              "txt-disabled",
					URL:               "https://github.com/user/repo/blob/main/http.txt",
					DefaultProtocol:   "http",
					AutoUpdateEnabled: false,
				},
			},
		},
	}

	feed, err := manager.findTXTFeedRequestByFeedKey("txt:https://raw.githubusercontent.com/user/repo/main/http.txt")
	if err != nil {
		t.Fatalf("findTXTFeedRequestByFeedKey() error = %v", err)
	}

	if feed.Kind != feedKindTXT {
		t.Fatalf("feed.Kind = %q, want %q", feed.Kind, feedKindTXT)
	}

	if feed.Name != "txt-disabled" {
		t.Fatalf("feed.Name = %q, want %q", feed.Name, "txt-disabled")
	}
}

func TestFindTXTFeedRequestByFeedKeyRejectsLegacyFeed(t *testing.T) {
	manager := &Manager{
		baseCfg: &config.Config{
			Subscriptions: []string{"https://example.com/sub"},
		},
	}

	_, err := manager.findTXTFeedRequestByFeedKey("legacy:https://example.com/sub")
	if err == nil {
		t.Fatal("findTXTFeedRequestByFeedKey() error = nil, want non-nil")
	}
}
