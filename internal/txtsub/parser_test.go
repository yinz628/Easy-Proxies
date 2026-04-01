package txtsub

import "testing"

func TestNormalizeSourceURLConvertsGitHubBlobToRaw(t *testing.T) {
	got := NormalizeSourceURL("https://github.com/vmheaven/VMHeaven-Free-Proxy-Updated/blob/main/all_proxies.txt")
	want := "https://raw.githubusercontent.com/vmheaven/VMHeaven-Free-Proxy-Updated/main/all_proxies.txt"
	if got != want {
		t.Fatalf("NormalizeSourceURL() = %q, want %q", got, want)
	}
}

func TestParseContentKeepsExplicitSchemes(t *testing.T) {
	result, err := ParseContent("http://1.1.1.1:80\nhttps://2.2.2.2:443\nsocks5://3.3.3.3:1080\n", "feed-a", DefaultProtocolHTTP)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}

	if len(result.Entries) != 3 {
		t.Fatalf("len(result.Entries) = %d, want 3", len(result.Entries))
	}

	if got, want := result.Entries[0].URI, "http://1.1.1.1:80"; got != want {
		t.Fatalf("entry[0].URI = %q, want %q", got, want)
	}
	if got, want := result.Entries[1].URI, "https://2.2.2.2:443"; got != want {
		t.Fatalf("entry[1].URI = %q, want %q", got, want)
	}
	if got, want := result.Entries[2].URI, "socks5://3.3.3.3:1080"; got != want {
		t.Fatalf("entry[2].URI = %q, want %q", got, want)
	}
}

func TestParseContentAppliesDefaultProtocolToHostPort(t *testing.T) {
	result, err := ParseContent("1.0.171.213:8080\n", "vmheaven", DefaultProtocolSOCKS5)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("len(result.Entries) = %d, want 1", len(result.Entries))
	}

	if got, want := result.Entries[0].URI, "socks5://1.0.171.213:8080"; got != want {
		t.Fatalf("entry[0].URI = %q, want %q", got, want)
	}

	if got, want := result.Entries[0].Name, "vmheaven-1.0.171.213:8080"; got != want {
		t.Fatalf("entry[0].Name = %q, want %q", got, want)
	}
}

func TestParseContentSkipsInvalidLines(t *testing.T) {
	result, err := ParseContent("ftp://1.1.1.1:21\nnot-a-proxy\nhttp://4.4.4.4:8080\n", "feed-b", DefaultProtocolHTTP)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("len(result.Entries) = %d, want 1", len(result.Entries))
	}

	if got, want := result.SkippedLines, 2; got != want {
		t.Fatalf("result.SkippedLines = %d, want %d", got, want)
	}

	if len(result.ErrorSamples) != 2 {
		t.Fatalf("len(result.ErrorSamples) = %d, want 2", len(result.ErrorSamples))
	}
}
