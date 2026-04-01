package txtsub

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

type DefaultProtocol string

const (
	DefaultProtocolHTTP   DefaultProtocol = "http"
	DefaultProtocolHTTPS  DefaultProtocol = "https"
	DefaultProtocolSOCKS5 DefaultProtocol = "socks5"
)

type Subscription struct {
	Name              string
	URL               string
	DefaultProtocol   DefaultProtocol
	AutoUpdateEnabled bool
}

type FeedKind string

const (
	FeedKindLegacy FeedKind = "legacy"
	FeedKindTXT    FeedKind = "txt"
)

type ParsedEntry struct {
	URI  string
	Name string
}

type ParseResult struct {
	NormalizedURL string
	Entries       []ParsedEntry
	SkippedLines  int
	ErrorSamples  []string
}

func NormalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	if !strings.EqualFold(u.Host, "github.com") {
		return raw
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "blob" {
		return raw
	}

	u.Scheme = "https"
	u.Host = "raw.githubusercontent.com"
	u.Path = path.Join(parts[0], parts[1], parts[3], path.Join(parts[4:]...))
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func NormalizeDefaultProtocol(value string) (DefaultProtocol, error) {
	switch DefaultProtocol(strings.ToLower(strings.TrimSpace(value))) {
	case DefaultProtocolHTTP:
		return DefaultProtocolHTTP, nil
	case DefaultProtocolHTTPS:
		return DefaultProtocolHTTPS, nil
	case DefaultProtocolSOCKS5:
		return DefaultProtocolSOCKS5, nil
	default:
		return "", fmt.Errorf("unsupported txt default protocol %q", value)
	}
}

func BuildFeedKey(kind FeedKind, normalizedURL string) string {
	return fmt.Sprintf("%s:%s", kind, strings.TrimSpace(normalizedURL))
}

func ParseContent(content, name string, defaultProtocol DefaultProtocol) (ParseResult, error) {
	normalizedProtocol, err := NormalizeDefaultProtocol(string(defaultProtocol))
	if err != nil {
		return ParseResult{}, err
	}

	result := ParseResult{}
	lines := strings.Split(content, "\n")
	seen := make(map[string]struct{})

	for idx, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, entryErr := parseLine(line, name, normalizedProtocol)
		if entryErr != nil {
			result.SkippedLines++
			if len(result.ErrorSamples) < 5 {
				result.ErrorSamples = append(result.ErrorSamples, fmt.Sprintf("line %d: %v", idx+1, entryErr))
			}
			continue
		}

		if _, ok := seen[entry.URI]; ok {
			continue
		}
		seen[entry.URI] = struct{}{}
		result.Entries = append(result.Entries, entry)
	}

	return result, nil
}

func parseLine(line, subscriptionName string, defaultProtocol DefaultProtocol) (ParsedEntry, error) {
	if strings.Contains(line, "://") {
		return parseExplicitURI(line, subscriptionName)
	}
	return parseHostPort(line, subscriptionName, defaultProtocol)
}

func parseExplicitURI(line, subscriptionName string) (ParsedEntry, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return ParsedEntry{}, fmt.Errorf("parse uri %q: %w", line, err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
	default:
		return ParsedEntry{}, fmt.Errorf("unsupported scheme in %q", line)
	}

	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" || port == "" {
		return ParsedEntry{}, fmt.Errorf("invalid host:port in %q", line)
	}

	if _, err := parsePort(port); err != nil {
		return ParsedEntry{}, fmt.Errorf("invalid port in %q", line)
	}

	return ParsedEntry{
		URI:  line,
		Name: buildEntryName(subscriptionName, host, port),
	}, nil
}

func parseHostPort(line, subscriptionName string, defaultProtocol DefaultProtocol) (ParsedEntry, error) {
	host, port, err := net.SplitHostPort(line)
	if err != nil {
		return ParsedEntry{}, fmt.Errorf("invalid host:port %q", line)
	}
	if host == "" {
		return ParsedEntry{}, fmt.Errorf("empty host in %q", line)
	}
	if _, err := parsePort(port); err != nil {
		return ParsedEntry{}, fmt.Errorf("invalid port in %q", line)
	}

	return ParsedEntry{
		URI:  fmt.Sprintf("%s://%s:%s", defaultProtocol, host, port),
		Name: buildEntryName(subscriptionName, host, port),
	}, nil
}

func parsePort(port string) (int, error) {
	value, err := net.LookupPort("tcp", port)
	if err != nil || value <= 0 || value > 65535 {
		return 0, fmt.Errorf("invalid port %q", port)
	}
	return value, nil
}

func buildEntryName(subscriptionName, host, port string) string {
	base := strings.TrimSpace(subscriptionName)
	if base == "" {
		base = "txt"
	}
	return fmt.Sprintf("%s-%s:%s", base, host, port)
}
