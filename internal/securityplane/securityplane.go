package securityplane

import (
	"net"
	"net/url"
	"strings"
	"time"

	"watcher/internal/model"
)

type EndpointConfig struct {
	EndpointID              string
	Role                    string
	BindAddr                string
	PublicURL               string
	OwnerTokenConfigured    bool
	DeviceAuthSupported     bool
	ServiceProxyConfigured  bool
	SecureCookies           *bool
	SessionSecretConfigured *bool
	HSTSEnabled             bool
	AllowedHosts            []string
	TrustedProxies          []string
	RateLimitPerMinute      int
	MaxBodyBytes            int64
}

func Build(scope string, endpoints []EndpointConfig) model.SecurityPosture {
	posture := model.SecurityPosture{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Scope:       strings.TrimSpace(scope),
		Endpoints:   make([]model.SecurityEndpointPosture, 0, len(endpoints)),
	}
	if posture.Scope == "" {
		posture.Scope = "system"
	}
	for _, endpoint := range endpoints {
		posture.Endpoints = append(posture.Endpoints, Endpoint(endpoint))
	}
	posture.Checklist = checklist(posture.Endpoints)
	return posture
}

func Endpoint(config EndpointConfig) model.SecurityEndpointPosture {
	scheme, urlHost := urlSchemeHost(config.PublicURL)
	bind := bindHost(config.BindAddr)
	host := urlHost
	if host == "" {
		host = bind
	}
	class := hostClass(host)
	exposure := exposureFor(class, bind, config.PublicURL)

	return model.SecurityEndpointPosture{
		EndpointID:              strings.TrimSpace(config.EndpointID),
		Role:                    strings.TrimSpace(config.Role),
		BindAddr:                strings.TrimSpace(config.BindAddr),
		PublicURL:               strings.TrimSpace(config.PublicURL),
		Scheme:                  scheme,
		Host:                    host,
		HostClass:               class,
		Exposure:                exposure,
		HTTPS:                   scheme == "https",
		HSTS:                    config.HSTSEnabled,
		OwnerTokenConfigured:    config.OwnerTokenConfigured,
		DeviceAuthSupported:     config.DeviceAuthSupported,
		ServiceProxyConfigured:  config.ServiceProxyConfigured,
		SecureCookies:           boolValue(config.SecureCookies),
		SessionSecretConfigured: boolValue(config.SessionSecretConfigured),
		AllowedHosts:            nonEmptyStrings(config.AllowedHosts),
		TrustedProxies:          nonEmptyStrings(config.TrustedProxies),
		RateLimitPerMinute:      config.RateLimitPerMinute,
		MaxBodyBytes:            config.MaxBodyBytes,
	}
}

func checklist(endpoints []model.SecurityEndpointPosture) []model.SecurityChecklistItem {
	publicEndpoint := false
	publicCleartext := false
	publicHTTPS := false
	publicBind := false
	ownerTokenConfigured := false
	allowedHostsReady := true
	trustedProxyReady := true
	hstsReady := true
	secureCookiesReady := true
	sessionSecretReady := true

	for _, endpoint := range endpoints {
		if endpoint.OwnerTokenConfigured {
			ownerTokenConfigured = true
		}
		publicLike := endpoint.Exposure == "public" || endpoint.Exposure == "public_bind"
		if publicLike {
			publicEndpoint = true
			if !endpoint.HTTPS {
				publicCleartext = true
			}
			if endpoint.HTTPS {
				publicHTTPS = true
			}
			if len(endpoint.AllowedHosts) == 0 || containsWildcard(endpoint.AllowedHosts) {
				allowedHostsReady = false
			}
		}
		if endpoint.Exposure == "public_bind" {
			publicBind = true
		}
		if endpoint.HTTPS && !endpoint.HSTS {
			hstsReady = false
		}
		if endpoint.HTTPS && len(endpoint.TrustedProxies) == 0 {
			trustedProxyReady = false
		}
		if endpoint.HTTPS && !endpoint.SecureCookies && endpoint.Role == "service" {
			secureCookiesReady = false
		}
		if endpoint.Role == "service" && !endpoint.SessionSecretConfigured {
			sessionSecretReady = false
		}
	}

	out := []model.SecurityChecklistItem{
		item("owner_token", passFail(ownerTokenConfigured), "critical", "Owner bearer token is configured."),
		item("session_secret", passFail(sessionSecretReady), "high", "Dashboard session signing secret is configured."),
	}
	if publicCleartext {
		out = append(out, item("public_https", "fail", "critical", "A public or wildcard endpoint is reachable without HTTPS."))
	} else {
		out = append(out, item("public_https", "pass", "critical", "No public cleartext endpoint was detected."))
	}
	if publicBind {
		out = append(out, item("direct_public_bind", "warn", "high", "An endpoint binds to all interfaces; firewall direct service ports before release."))
	} else {
		out = append(out, item("direct_public_bind", "pass", "high", "No wildcard public bind was detected."))
	}
	if publicEndpoint && !allowedHostsReady {
		out = append(out, item("allowed_hosts", "warn", "high", "Public endpoints should restrict Host headers to expected names."))
	} else {
		out = append(out, item("allowed_hosts", "pass", "high", "Allowed host posture is acceptable for the detected exposure."))
	}
	if publicHTTPS && !trustedProxyReady {
		out = append(out, item("trusted_proxies", "warn", "medium", "HTTPS edge deployments should list trusted proxy ranges."))
	} else {
		out = append(out, item("trusted_proxies", "pass", "medium", "Trusted proxy posture is acceptable for the detected exposure."))
	}
	if publicHTTPS && !hstsReady {
		out = append(out, item("hsts", "warn", "medium", "Enable HSTS after HTTPS is stable."))
	} else {
		out = append(out, item("hsts", "pass", "medium", "HSTS posture is acceptable for the detected exposure."))
	}
	if !secureCookiesReady {
		out = append(out, item("secure_cookies", "fail", "high", "Secure dashboard cookies are required for public HTTPS."))
	} else {
		out = append(out, item("secure_cookies", "pass", "high", "Secure cookie posture is acceptable for the detected exposure."))
	}
	return out
}

func item(id, status, severity, message string) model.SecurityChecklistItem {
	return model.SecurityChecklistItem{
		ItemID:   id,
		Status:   status,
		Severity: severity,
		Message:  message,
	}
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func urlSchemeHost(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", ""
	}
	return strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Hostname())
}

func bindHost(bindAddr string) string {
	bindAddr = strings.TrimSpace(bindAddr)
	if bindAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(bindAddr)
	if err == nil {
		return strings.ToLower(strings.Trim(host, "[]"))
	}
	if strings.Contains(bindAddr, ":") && net.ParseIP(strings.Trim(bindAddr, "[]")) == nil {
		return ""
	}
	return strings.ToLower(strings.Trim(bindAddr, "[]"))
}

func exposureFor(class string, bind string, publicURL string) string {
	if class == "unconfigured" {
		return "unconfigured"
	}
	if isWildcard(bind) {
		return "public_bind"
	}
	switch class {
	case "local":
		return "local_dev"
	case "private_lan":
		return "lan_private"
	default:
		if strings.TrimSpace(publicURL) == "" {
			return "public_bind"
		}
		return "public"
	}
}

func hostClass(host string) string {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "" {
		return "unconfigured"
	}
	if isWildcard(host) {
		return "wildcard"
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "10.0.2.2" {
		return "local"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") {
			return "private_lan"
		}
		return "public"
	}
	if ip.IsLoopback() {
		return "local"
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return "private_lan"
	}
	return "public"
}

func isWildcard(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	return host == "*" || host == "0.0.0.0" || host == "::"
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func containsWildcard(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "*" {
			return true
		}
	}
	return false
}

func boolValue(value *bool) bool {
	return value != nil && *value
}
