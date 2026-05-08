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
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		trimmed := strings.ToLower(strings.TrimSpace(host))
		if trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := strings.ToLower(stripPort(r.Host))
			if _, ok := allowed[host]; !ok {
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
