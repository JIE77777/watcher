package netpolicy

import (
	"net/http"
	"os"
	"strings"
	"time"
)

var proxyEnvKeys = map[string]struct{}{
	"HTTP_PROXY":  {},
	"HTTPS_PROXY": {},
	"ALL_PROXY":   {},
	"NO_PROXY":    {},
}

// DirectHTTPClient returns an HTTP client that never uses environment proxies.
func DirectHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil

	client := &http.Client{Transport: transport}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

// StripProxyEnv removes common proxy environment variables from a process env.
func StripProxyEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		if _, blocked := proxyEnvKeys[strings.ToUpper(name)]; blocked {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func CurrentEnvWithoutProxy() []string {
	return StripProxyEnv(os.Environ())
}
