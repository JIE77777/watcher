package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	installCookieName = "watcher_install"
	installCookieTTL  = 15 * time.Minute
)

func (a *App) handleInstallPage(w http.ResponseWriter, r *http.Request) {
	if a.cfg.AppRelease.APKPath == "" || a.cfg.AppRelease.VersionCode <= 0 {
		http.Error(w, "app release not configured", http.StatusNotFound)
		return
	}
	fingerprint := ""
	if a.cfg.Security.TLS.Enabled {
		fingerprint, _ = relayCertificateFingerprint(a.cfg.Security.TLS.CertFile)
	}
	data := map[string]string{
		"Version":     firstNonBlank(a.cfg.AppRelease.VersionName, strconv.Itoa(a.cfg.AppRelease.VersionCode)),
		"Download":    "/install/apk",
		"RelayURL":    publicURLForRequest(r),
		"Fingerprint": fingerprint,
		"Authorized":  strconv.FormatBool(a.installAuthorized(r)),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = installTemplate.Execute(w, data)
}

func (a *App) handleInstallSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(r.PostFormValue("owner_token"))
	if !tokenMatches(token, a.cfg.OwnerToken) || a.cfg.OwnerToken == "" {
		http.Error(w, "invalid owner token", http.StatusUnauthorized)
		return
	}
	expires := time.Now().Add(installCookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     installCookieName,
		Value:    a.signInstallCookie(expires),
		Path:     "/install",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || a.cfg.Security.TLS.Enabled,
		Expires:  expires,
		MaxAge:   int(installCookieTTL.Seconds()),
	})
	http.Redirect(w, r, "/install", http.StatusSeeOther)
}

func (a *App) handleInstallAPK(w http.ResponseWriter, r *http.Request) {
	if !a.installAuthorized(r) {
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/install", http.StatusSeeOther)
			return
		}
		http.Error(w, "install authorization required", http.StatusUnauthorized)
		return
	}
	a.serveAppReleaseAPK(w, r)
}

func (a *App) installAuthorized(r *http.Request) bool {
	if tokenMatches(bearerToken(r), a.cfg.OwnerToken) && a.cfg.OwnerToken != "" {
		return true
	}
	cookie, err := r.Cookie(installCookieName)
	if err != nil {
		return false
	}
	return a.verifyInstallCookie(cookie.Value, time.Now())
}

func (a *App) signInstallCookie(expires time.Time) string {
	expiresUnix := expires.Unix()
	body := strconv.FormatInt(expiresUnix, 10)
	mac := hmac.New(sha256.New, []byte(a.cfg.OwnerToken))
	_, _ = mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s.%s", body, sig)
}

func (a *App) verifyInstallCookie(value string, now time.Time) bool {
	expiresText, sig, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok || expiresText == "" || sig == "" || a.cfg.OwnerToken == "" {
		return false
	}
	expiresUnix, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || now.Unix() > expiresUnix {
		return false
	}
	expected := a.signInstallCookie(time.Unix(expiresUnix, 0))
	return subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

func (a *App) handleTLSInfoV2(w http.ResponseWriter, _ *http.Request) {
	fingerprint := ""
	if a.cfg.Security.TLS.Enabled {
		fingerprint, _ = relayCertificateFingerprint(a.cfg.Security.TLS.CertFile)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     a.cfg.Security.TLS.Enabled,
		"fingerprint": fingerprint,
	})
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var installTemplate = template.Must(template.New("install").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Watcher Install</title>
  <style>
    body { margin: 0; font: 16px system-ui, sans-serif; background: #f5f7fa; color: #0f172a; }
    main { max-width: 720px; margin: 0 auto; padding: 28px 18px; }
    h1 { margin: 0 0 8px; font-size: 30px; }
    p { color: #475569; line-height: 1.5; }
    a.button, button { display: block; box-sizing: border-box; width: 100%; margin: 20px 0; padding: 14px 16px; background: #0f172a; color: white; text-align: center; text-decoration: none; border: 0; border-radius: 8px; font: inherit; font-weight: 700; }
    input { box-sizing: border-box; width: 100%; padding: 12px; border: 1px solid #cbd5e1; border-radius: 8px; font: inherit; }
    code { display: block; overflow-wrap: anywhere; background: white; padding: 12px; border: 1px solid #dbe3ef; border-radius: 8px; color: #334155; }
    section { margin-top: 18px; }
  </style>
</head>
<body>
<main>
  <h1>Watcher</h1>
  <p>Install the Android app, then use the relay URL and owner token from your deployment output to register this device.</p>
  {{if eq .Authorized "true"}}
  <a class="button" href="{{.Download}}">Download Android APK {{.Version}}</a>
  {{else}}
  <form method="post" action="/install/session">
    <p>Enter owner token to unlock a short install session.</p>
    <input name="owner_token" type="password" autocomplete="current-password" required>
    <button type="submit">Unlock Download {{.Version}}</button>
  </form>
  {{end}}
  <section>
    <p>Relay URL</p>
    <code>{{.RelayURL}}</code>
  </section>
  {{if .Fingerprint}}
  <section>
    <p>TLS fingerprint</p>
    <code>{{.Fingerprint}}</code>
  </section>
  {{end}}
</main>
</body>
</html>`))
