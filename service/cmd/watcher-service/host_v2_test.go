package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"watcher/internal/store"
	"watcher/pkg/serverguard"
)

func TestHostFilesListAndDownloadUseAllowlistedRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.cfg.Shell.ManifestPath = filepath.Join(root, "watcher.shell.json")
	app.cfg.Host.FileRoots = []HostFileRootConfig{
		{ID: "reports", Label: "Reports", Path: root, Download: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/host/files?root=reports", nil)
	rec := httptest.NewRecorder()
	app.handleHostFilesV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Entries []hostFileEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "report.txt" || !listing.Entries[0].Download {
		t.Fatalf("unexpected entries: %+v", listing.Entries)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v2/modules/host/files/download?root=reports&path=report.txt", nil)
	rec = httptest.NewRecorder()
	app.handleHostFileDownloadV2(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("download status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHostFilesRejectTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.cfg.Shell.ManifestPath = filepath.Join(root, "watcher.shell.json")
	app.cfg.Host.FileRoots = []HostFileRootConfig{
		{ID: "root", Label: "Root", Path: root, Download: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/host/files/download?root=root&path=../secret.txt", nil)
	rec := httptest.NewRecorder()
	app.handleHostFileDownloadV2(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHostFilesRejectSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "secret-link.txt")); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.cfg.Shell.ManifestPath = filepath.Join(root, "watcher.shell.json")
	app.cfg.Host.FileRoots = []HostFileRootConfig{
		{ID: "root", Label: "Root", Path: root, Download: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/host/files/download?root=root&path=secret-link.txt", nil)
	rec := httptest.NewRecorder()
	app.handleHostFileDownloadV2(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestLoadConfigDefaultHostRootsDownloadWorkspace(t *testing.T) {
	repo := t.TempDir()
	configDir := filepath.Join(repo, "service")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Host.FileRoots) < 2 {
		t.Fatalf("default roots = %+v, want workspace and releases", cfg.Host.FileRoots)
	}
	if cfg.Host.FileRoots[0].ID != "workspace" || !cfg.Host.FileRoots[0].Download {
		t.Fatalf("first default root = %+v, want downloadable workspace", cfg.Host.FileRoots[0])
	}
	if cfg.Host.FileRoots[1].ID != "releases" || !cfg.Host.FileRoots[1].Download {
		t.Fatalf("second default root = %+v, want downloadable releases", cfg.Host.FileRoots[1])
	}
}

func TestHostFilesDirectoryEntryTargetsNestedRoot(t *testing.T) {
	workspace := t.TempDir()
	releases := filepath.Join(workspace, "releases")
	if err := os.MkdirAll(releases, 0o755); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.cfg.Shell.ManifestPath = filepath.Join(workspace, "watcher.shell.json")
	app.cfg.Host.FileRoots = []HostFileRootConfig{
		{ID: "workspace", Label: "Workspace", Path: workspace, Download: false},
		{ID: "releases", Label: "Releases", Path: releases, Download: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/host/files?root=workspace", nil)
	rec := httptest.NewRecorder()
	app.handleHostFilesV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Entries []hostFileEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "releases" {
		t.Fatalf("unexpected entries: %+v", listing.Entries)
	}
	if listing.Entries[0].TargetRootID != "releases" || !listing.Entries[0].TargetDownload {
		t.Fatalf("target root metadata = %+v, want downloadable releases root", listing.Entries[0])
	}
}

func TestUnknownAPIRouteDoesNotFallThroughToDashboard(t *testing.T) {
	ipResolver, err := serverguard.NewIPResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		cfg: Config{
			Security: SecurityConfig{
				MaxBodyBytes:             1 << 20,
				GlobalRateLimitPerMinute: 100,
				LoginRateLimitPerMinute:  100,
				SessionSecret:            "test-secret",
				SessionTTLSeconds:        60,
			},
		},
		sessionSigner: serverguard.NewSigner("test-secret"),
		ipResolver:    ipResolver,
		globalLimiter: serverguard.NewMemoryRateLimiter(100, time.Minute),
		loginLimiter:  serverguard.NewMemoryRateLimiter(100, time.Minute),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/host/files/download?root=workspace&path=file.txt", nil)
	rec := httptest.NewRecorder()
	app.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestHostFileRootCreatePersistsCustomRoot(t *testing.T) {
	repo := t.TempDir()
	custom := filepath.Join(repo, "custom")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "artifact.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	localStore, err := store.OpenLocal(filepath.Join(repo, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer localStore.Close()

	app := &App{store: localStore}
	app.cfg.Shell.ManifestPath = filepath.Join(repo, "watcher.shell.json")
	app.cfg.Host.FileRoots = []HostFileRootConfig{
		{ID: "releases", Label: "Releases", Path: repo, Download: false},
	}
	body := []byte(`{"label":"Artifacts","path":"` + filepath.ToSlash(custom) + `","download":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/modules/host/file-roots", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	app.handleHostFileRootCreateV2(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Root hostFileRoot `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created root: %v", err)
	}
	if created.Root.ID == "" || created.Root.Source != "custom" || !created.Root.Removable {
		t.Fatalf("unexpected created root: %+v", created.Root)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v2/modules/host/files?root="+created.Root.ID, nil)
	rec = httptest.NewRecorder()
	app.handleHostFilesV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Entries []hostFileEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "artifact.txt" || !listing.Entries[0].Download {
		t.Fatalf("unexpected entries: %+v", listing.Entries)
	}
}
