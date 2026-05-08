package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"watcher/internal/model"
)

func TestInitiatorHeadersForDevice(t *testing.T) {
	headers := initiatorHeadersForDevice(model.DeviceRegistration{
		DeviceID:   " phone-1 \n primary ",
		Platform:   "android",
		DeviceName: strings.Repeat("p", initiatorValueLimit+8),
	})
	if headers[initiatorHeaderType] != "device" || headers[initiatorHeaderVia] != "relay" {
		t.Fatalf("headers = %+v, want device via relay", headers)
	}
	if headers[initiatorHeaderDevice] != "phone-1 primary" || headers[initiatorHeaderOS] != "android" {
		t.Fatalf("identity headers = %+v", headers)
	}
	if len([]rune(headers[initiatorHeaderName])) != initiatorValueLimit {
		t.Fatalf("device name length = %d, want %d", len([]rune(headers[initiatorHeaderName])), initiatorValueLimit)
	}
}

func TestInitiatorHeadersForOwner(t *testing.T) {
	headers := initiatorHeadersForDevice(model.DeviceRegistration{DeviceID: "owner"})
	if headers[initiatorHeaderType] != "owner" || headers[initiatorHeaderDevice] != "owner" || headers[initiatorHeaderVia] != "relay" {
		t.Fatalf("owner headers = %+v", headers)
	}
}

func TestInstallCookieAuthorizesAPKDownload(t *testing.T) {
	app := &App{}
	app.cfg.OwnerToken = "owner-token"
	app.cfg.AppRelease.VersionCode = 1
	app.cfg.AppRelease.APKPath = "/tmp/watcher.apk"

	req := httptest.NewRequest(http.MethodGet, "/install/apk", nil)
	if app.installAuthorized(req) {
		t.Fatalf("request without cookie was authorized")
	}

	expires := time.Now().Add(time.Minute)
	req.AddCookie(&http.Cookie{Name: installCookieName, Value: app.signInstallCookie(expires)})
	if !app.installAuthorized(req) {
		t.Fatalf("valid install cookie was rejected")
	}

	expiredReq := httptest.NewRequest(http.MethodGet, "/install/apk", nil)
	expiredReq.AddCookie(&http.Cookie{Name: installCookieName, Value: app.signInstallCookie(time.Now().Add(-time.Minute))})
	if app.installAuthorized(expiredReq) {
		t.Fatalf("expired install cookie was authorized")
	}
}
