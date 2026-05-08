package main

import (
	"net/http"
	"strings"

	"watcher/internal/model"
	"watcher/internal/securityplane"
)

func (a *App) handleSecurityPostureV2(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"posture": a.securityPosture()})
}

func (a *App) securityPosture() model.SecurityPosture {
	secureCookies := a.cfg.Security.SecureCookies
	sessionSecretConfigured := strings.TrimSpace(a.cfg.Security.SessionSecret) != ""
	return securityplane.Build("service", []securityplane.EndpointConfig{
		{
			EndpointID:              "service",
			Role:                    "service",
			BindAddr:                a.cfg.BindAddr,
			OwnerTokenConfigured:    strings.TrimSpace(a.cfg.OwnerToken) != "",
			SecureCookies:           &secureCookies,
			SessionSecretConfigured: &sessionSecretConfigured,
			HSTSEnabled:             a.cfg.Security.EnableHSTS,
			AllowedHosts:            a.cfg.Security.AllowedHosts,
			TrustedProxies:          a.cfg.Security.TrustedProxies,
			RateLimitPerMinute:      a.cfg.Security.GlobalRateLimitPerMinute,
			MaxBodyBytes:            a.cfg.Security.MaxBodyBytes,
		},
	})
}
