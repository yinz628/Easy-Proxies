package monitor

import (
	"net/url"
	"strconv"
	"strings"
)

type importNamingState struct {
	reservedNames  map[string]struct{}
	prefixCounters map[string]int
}

func newImportNamingState(existingNames []string) *importNamingState {
	state := &importNamingState{
		reservedNames:  make(map[string]struct{}, len(existingNames)),
		prefixCounters: make(map[string]int),
	}

	for _, name := range existingNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		state.reservedNames[trimmed] = struct{}{}
		if prefix, suffix, ok := splitImportSuffix(trimmed); ok && suffix > state.prefixCounters[prefix] {
			state.prefixCounters[prefix] = suffix
		}
	}

	return state
}

func resolveImportedNodeName(candidateName, prefix string, reserved map[string]struct{}, counters map[string]int) (string, bool, string) {
	normalizedPrefix := normalizeImportNamePrefix(prefix)
	trimmedCandidate := strings.TrimSpace(candidateName)
	if trimmedCandidate != "" {
		if _, exists := reserved[trimmedCandidate]; !exists {
			reserved[trimmedCandidate] = struct{}{}
			return trimmedCandidate, false, ""
		}
	}

	reason := "missing_name"
	if trimmedCandidate != "" {
		reason = "name_conflict"
	}

	finalName := nextPrefixedImportName(normalizedPrefix, reserved, counters)
	return finalName, true, reason
}

func nextPrefixedImportName(prefix string, reserved map[string]struct{}, counters map[string]int) string {
	next := counters[prefix]
	for {
		next++
		name := prefix + "-" + strconv.Itoa(next)
		if _, exists := reserved[name]; exists {
			continue
		}
		counters[prefix] = next
		reserved[name] = struct{}{}
		return name
	}
}

func normalizeImportNamePrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return "imported"
	}
	return trimmed
}

func splitImportSuffix(name string) (string, int, bool) {
	idx := strings.LastIndex(name, "-")
	if idx <= 0 || idx >= len(name)-1 {
		return "", 0, false
	}

	suffix, err := strconv.Atoi(name[idx+1:])
	if err != nil || suffix <= 0 {
		return "", 0, false
	}

	return name[:idx], suffix, true
}

func extractImportNodeName(rawURI string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil || parsed.Fragment == "" {
		return ""
	}

	if decoded, decodeErr := url.QueryUnescape(parsed.Fragment); decodeErr == nil {
		return strings.TrimSpace(decoded)
	}

	return strings.TrimSpace(parsed.Fragment)
}
