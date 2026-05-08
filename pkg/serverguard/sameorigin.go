package serverguard

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func SameOrigin(allowMissing bool) Middleware {
	return SameOriginWithTrustedOrigins(allowMissing, nil)
}

func SameOriginWithTrustedOrigins(allowMissing bool, trustedOrigins []string) Middleware {
	trusted := make([]string, 0, len(trustedOrigins))
	for _, origin := range trustedOrigins {
		trimmed := normalizeHostPattern(origin)
		if trimmed != "" {
			trusted = append(trusted, trimmed)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			if matchesOriginHeader(r) || matchesRefererHeader(r) || matchesTrustedOriginHeader(r, trusted) || matchesTrustedRefererHeader(r, trusted) {
				next.ServeHTTP(w, r)
				return
			}
			if allowMissing && strings.TrimSpace(r.Header.Get("Origin")) == "" && strings.TrimSpace(r.Header.Get("Referer")) == "" {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "cross-site request blocked", http.StatusForbidden)
		})
	}
}

func matchesOriginHeader(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	return sameOrigin(origin, r.Host)
}

func matchesRefererHeader(r *http.Request) bool {
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer == "" {
		return false
	}
	return sameOrigin(referer, r.Host)
}

func sameOrigin(rawURL, host string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	originHost := normalizeHostPattern(parsed.Host)
	requestHost := normalizeHostPattern(host)
	return strings.EqualFold(originHost, requestHost) || (isLoopbackHost(originHost) && isLoopbackHost(requestHost))
}

func matchesTrustedOriginHeader(r *http.Request, trusted []string) bool {
	return trustedOrigin(strings.TrimSpace(r.Header.Get("Origin")), trusted)
}

func matchesTrustedRefererHeader(r *http.Request, trusted []string) bool {
	return trustedOrigin(strings.TrimSpace(r.Header.Get("Referer")), trusted)
}

func trustedOrigin(rawURL string, trusted []string) bool {
	if rawURL == "" || len(trusted) == 0 {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return hostMatchesAny(parsed.Host, trusted)
}

func isLoopbackHost(host string) bool {
	normalized := strings.TrimSuffix(normalizeHostPattern(host), ".")
	if normalized == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(normalized, "[]"))
	return ip != nil && ip.IsLoopback()
}
