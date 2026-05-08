package serverguard

import (
	"net/http"
	"net/url"
	"strings"
)

func SameOrigin(allowMissing bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			if matchesOriginHeader(r) || matchesRefererHeader(r) {
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
	return strings.EqualFold(stripPort(parsed.Host), stripPort(host))
}
