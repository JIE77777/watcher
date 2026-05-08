package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"watcher/internal/model"
	"watcher/internal/store"
)

func TestBuildModuleDescriptorsUsesManifestContract(t *testing.T) {
	target := model.ShellTarget{ComponentID: "opencode", Surface: "sessions"}
	modules := buildModuleDescriptors([]model.ComponentStatus{{
		Manifest: model.ComponentManifest{
			ID:              "opencode",
			Name:            "Opencode",
			Version:         "0.1.0",
			Stage:           "prototype",
			RuntimeShape:    model.RuntimeShapeInProcess,
			Capabilities:    []string{"interactive_session"},
			Surfaces:        []model.ModuleSurface{{ID: "sessions", Kind: "collection", Primary: true, Target: target}},
			DefaultTarget:   &target,
			Actions:         []model.ModuleAction{{ActionID: "mirror.message", Label: "Send message", Kind: "submit", OperationName: "mirror.message", Async: true}},
			Streams:         []string{"opencode.session"},
			Resources:       []string{"session"},
			Operations:      []string{"mirror.message"},
			AndroidSurfaces: []string{"opencode_sessions"},
		},
		ManifestValid: true,
		RuntimeStatus: model.RuntimeStatusReady,
	}})

	if len(modules) != 1 {
		t.Fatalf("len(modules) = %d, want 1", len(modules))
	}
	module := modules[0]
	if module.ComponentID != "opencode" || module.DefaultTarget.Surface != "sessions" {
		t.Fatalf("unexpected module descriptor: %#v", module)
	}
	if module.Status != model.RuntimeStatusReady {
		t.Fatalf("status = %q, want %q", module.Status, model.RuntimeStatusReady)
	}
	if len(module.Capabilities) != 1 || module.Capabilities[0] != "interactive_session" {
		t.Fatalf("unexpected capabilities: %#v", module.Capabilities)
	}
	if len(module.Surfaces) != 1 || module.Surfaces[0].Kind != "collection" {
		t.Fatalf("unexpected surfaces: %#v", module.Surfaces)
	}
	if len(module.Actions) != 1 || module.Actions[0].ActionID != "mirror.message" {
		t.Fatalf("unexpected actions: %#v", module.Actions)
	}
}

func TestBuildModuleDescriptorsDerivesLegacyAndroidSurfaces(t *testing.T) {
	modules := buildModuleDescriptors([]model.ComponentStatus{{
		Manifest: model.ComponentManifest{
			ID:              "box",
			Name:            "Box",
			Version:         "0.1.0",
			Stage:           "active",
			AndroidSurfaces: []string{"main_feed"},
		},
		RuntimeStatus: model.RuntimeStatusReady,
	}})

	if len(modules) != 1 {
		t.Fatalf("len(modules) = %d, want 1", len(modules))
	}
	module := modules[0]
	if len(module.Surfaces) != 1 {
		t.Fatalf("len(surfaces) = %d, want 1", len(module.Surfaces))
	}
	if module.Surfaces[0].Kind != "legacy_android" || !module.Surfaces[0].Primary {
		t.Fatalf("unexpected derived surface: %#v", module.Surfaces[0])
	}
	if module.DefaultTarget.Surface != "main_feed" {
		t.Fatalf("default target surface = %q, want main_feed", module.DefaultTarget.Surface)
	}
}

func TestHandleModuleV2ReturnsDescriptor(t *testing.T) {
	app := testModuleApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode", nil)
	req.SetPathValue("componentID", "opencode")
	rec := httptest.NewRecorder()

	app.handleModuleV2(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"component_id":"opencode"`) {
		t.Fatalf("response does not contain opencode descriptor: %s", rec.Body.String())
	}
}

func testModuleApp(t *testing.T) *App {
	t.Helper()
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = localStore.Close() })

	repoRoot := filepath.Join("..", "..", "..")
	app := &App{store: localStore}
	app.cfg.Shell.ManifestPath = filepath.Join(repoRoot, "watcher.shell.json")
	app.cfg.Shell.VersionFile = filepath.Join(repoRoot, "VERSION")
	app.cfg.Shell.ComponentsRoot = filepath.Join(repoRoot, "modules")
	return app
}
