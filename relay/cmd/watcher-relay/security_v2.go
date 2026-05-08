package main

import (
	"net/http"
	"strings"

	"watcher/internal/model"
	"watcher/internal/securityplane"
)

func (a *App) handleSecurityPostureV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"posture": a.securityPosture(r)})
}

func (a *App) securityPosture(r *http.Request) model.SecurityPosture {
	return securityplane.Build("relay", []securityplane.EndpointConfig{
		{
			EndpointID:             "relay",
			Role:                   "relay",
			BindAddr:               a.cfg.BindAddr,
			PublicURL:              publicURLForRequest(r),
			OwnerTokenConfigured:   strings.TrimSpace(a.cfg.OwnerToken) != "",
			DeviceAuthSupported:    true,
			HSTSEnabled:            a.cfg.Security.EnableHSTS,
			AllowedHosts:           a.cfg.Security.AllowedHosts,
			TrustedProxies:         a.cfg.Security.TrustedProxies,
			RateLimitPerMinute:     a.cfg.Security.GlobalRateLimitPerMinute,
			MaxBodyBytes:           a.cfg.Security.MaxBodyBytes,
			ServiceProxyConfigured: strings.TrimSpace(a.cfg.Service.BaseURL) != "",
		},
		{
			EndpointID:             "service_proxy",
			Role:                   "service_proxy",
			PublicURL:              strings.TrimSpace(a.cfg.Service.BaseURL),
			OwnerTokenConfigured:   strings.TrimSpace(a.cfg.Service.OwnerToken) != "",
			ServiceProxyConfigured: strings.TrimSpace(a.cfg.Service.BaseURL) != "",
		},
	})
}

func publicURLForRequest(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := firstForwardedValue(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return strings.ToLower(scheme) + "://" + host
}

func firstForwardedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if before, _, ok := strings.Cut(value, ","); ok {
		value = before
	}
	return strings.ToLower(strings.TrimSpace(value))
}
