package serverguard

import (
	"net"
	"net/http"
	"strings"
)

func AllowedHosts(hosts []string) Middleware {
	if len(hosts) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(hosts))
	for _, host := range hosts {
		trimmed := normalizeHostPattern(host)
		if trimmed != "" {
			allowed = append(allowed, trimmed)
		}
	}
	if len(allowed) == 0 {
		return nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := normalizeHostPattern(r.Host)
			if !hostMatchesAny(host, allowed) {
				http.Error(w, "host not allowed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func stripPort(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

func normalizeHostPattern(host string) string {
	trimmed := strings.ToLower(strings.TrimSpace(host))
	if strings.Contains(trimmed, "://") {
		if parsed, err := http.NewRequest(http.MethodGet, trimmed, nil); err == nil {
			trimmed = parsed.URL.Host
		}
	}
	return strings.TrimSpace(stripPort(trimmed))
}

func hostMatchesAny(host string, patterns []string) bool {
	host = normalizeHostPattern(host)
	if host == "" {
		return false
	}
	for _, pattern := range patterns {
		if hostMatchesPattern(host, pattern) {
			return true
		}
	}
	return false
}

func hostMatchesPattern(host, pattern string) bool {
	pattern = normalizeHostPattern(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*.")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}
