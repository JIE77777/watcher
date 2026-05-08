package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"watcher/internal/model"
)

func TestLoadShellStatusAndDiscoverComponents(t *testing.T) {
	root := t.TempDir()
	modulesRoot := filepath.Join(root, "modules")
	if err := os.MkdirAll(filepath.Join(modulesRoot, "codex"), 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(modulesRoot, "box"), 0o755); err != nil {
		t.Fatalf("mkdir box: %v", err)
	}
	writeFile(t, filepath.Join(modulesRoot, "codex", "README.md"), "# Codex\n")
	writeFile(t, filepath.Join(modulesRoot, "box", "README.md"), "# Box\n")
	writeFile(t, filepath.Join(root, "watcher.shell.json"), `{
  "id": "watcher.shell",
  "name": "Watcher Shell",
  "stage": "active",
  "contract_version": "v2",
  "release_line": "shell",
  "release_channel": "public_preview",
  "runtime_defaults": {
    "light_component_runtime": "in_process",
    "heavy_component_runtime": "worker"
  },
  "worker_contract": {
    "version": "v1",
    "spawn_model": "shell_managed",
    "health_model": "process_and_healthcheck",
    "log_model": "stdio",
    "event_model": "typed_envelope",
    "operation_model": "shell_routed"
  }
}`)
	writeFile(t, filepath.Join(root, "VERSION"), "0.2.0\n")
	writeFile(t, filepath.Join(modulesRoot, "codex", ComponentManifestFile), `{
  "id": "codex",
  "name": "Codex",
  "version": "0.2.0",
  "stage": "active",
  "release_line": "component-codex",
  "release_channel": "public_preview",
  "shell_contract": "v2",
  "component_class": "light",
  "runtime_shape": "in_process",
  "runtime_owner": "watcher-service",
  "shell_dependencies": ["event_bus"],
  "docs": ["modules/codex/README.md"]
}`)
	writeFile(t, filepath.Join(modulesRoot, "box", ComponentManifestFile), `{
  "id": "box",
  "name": "Box",
  "version": "0.2.0",
  "stage": "active",
  "release_line": "component-box",
  "release_channel": "public_preview",
  "shell_contract": "v2",
  "component_class": "light",
  "runtime_shape": "in_process",
  "runtime_owner": "watcher-service",
  "shell_dependencies": ["event_bus"],
  "docs": ["modules/box/README.md"]
}`)

	shell, err := LoadShellStatus(filepath.Join(root, "watcher.shell.json"), filepath.Join(root, "VERSION"), modulesRoot)
	if err != nil {
		t.Fatalf("LoadShellStatus: %v", err)
	}
	if shell.Version != "0.2.0" {
		t.Fatalf("shell.Version = %q, want 0.2.0", shell.Version)
	}
	if shell.Manifest.ContractVersion != model.ShellContractV2 {
		t.Fatalf("contract = %q, want %s", shell.Manifest.ContractVersion, model.ShellContractV2)
	}

	statuses, err := DiscoverComponentStatuses(modulesRoot, shell.Manifest.ContractVersion)
	if err != nil {
		t.Fatalf("DiscoverComponentStatuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("len(statuses) = %d, want 2", len(statuses))
	}
	if statuses[0].Manifest.ID != "box" || statuses[1].Manifest.ID != "codex" {
		t.Fatalf("unexpected component order: %+v", statuses)
	}
	if err := ValidateComponentStatuses(statuses); err != nil {
		t.Fatalf("ValidateComponentStatuses: %v", err)
	}
	if !statuses[0].ManifestValid || !statuses[1].ManifestValid {
		t.Fatalf("expected manifest_valid for both components: %+v", statuses)
	}
	if !statuses[0].DocsPresent || !statuses[1].DocsPresent {
		t.Fatalf("expected docs_present for both components: %+v", statuses)
	}
	if !statuses[0].ShellContractCompatible || !statuses[1].ShellContractCompatible {
		t.Fatalf("expected shell contract compatibility: %+v", statuses)
	}
}

func TestDiscoverComponentStatusesMarksInvalidManifest(t *testing.T) {
	root := t.TempDir()
	modulesRoot := filepath.Join(root, "modules", "fixture")
	if err := os.MkdirAll(modulesRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(modulesRoot, ComponentManifestFile), `{
  "id": "fixture",
  "name": "Fixture"
}`)
	statuses, err := DiscoverComponentStatuses(filepath.Join(root, "modules"), model.ShellContractV2)
	if err != nil {
		t.Fatalf("DiscoverComponentStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].ManifestValid {
		t.Fatalf("expected invalid manifest status: %+v", statuses[0])
	}
	if err := ValidateComponentStatuses(statuses); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestValidateComponentManifestRequiresWorkerBlockForHeavyComponents(t *testing.T) {
	err := ValidateComponentManifest(model.ComponentManifest{
		ID:                "probe",
		Name:              "Probe",
		Version:           "0.1.0",
		Stage:             "prototype",
		ReleaseLine:       "component-probe",
		ReleaseChannel:    "public_preview",
		ShellContract:     model.ShellContractV2,
		ComponentClass:    model.ComponentClassHeavy,
		RuntimeShape:      model.RuntimeShapeWorker,
		RuntimeOwner:      "watcher-service",
		ShellDependencies: []string{"event_bus", "operation_contract"},
		Docs:              []string{"modules/probe/README.md"},
	})
	if err == nil {
		t.Fatalf("expected worker block validation error")
	}
}

func TestValidateComponentManifestValidatesModuleContractFields(t *testing.T) {
	target := model.ShellTarget{ComponentID: "other", Surface: "sessions"}
	err := ValidateComponentManifest(model.ComponentManifest{
		ID:                "opencode",
		Name:              "Opencode",
		Version:           "0.1.0",
		Stage:             "prototype",
		ReleaseLine:       "component-opencode",
		ReleaseChannel:    "public_preview",
		ShellContract:     model.ShellContractV2,
		ComponentClass:    model.ComponentClassLight,
		RuntimeShape:      model.RuntimeShapeInProcess,
		RuntimeOwner:      "watcher-service",
		Capabilities:      []string{"interactive_session", "interactive_session"},
		Streams:           []string{"opencode.session"},
		Resources:         []string{"session"},
		Operations:        []string{"mirror.message"},
		Surfaces:          []model.ModuleSurface{{ID: "sessions", Kind: "collection", Target: target}},
		DefaultTarget:     &target,
		Actions:           []model.ModuleAction{{ActionID: "mirror.message", Label: "Send", Kind: "submit", OperationName: "Mirror Message"}},
		ShellDependencies: []string{"event_bus"},
		Docs:              []string{"modules/opencode/README.md"},
	})
	if err == nil {
		t.Fatalf("expected module contract validation error")
	}
	message := err.Error()
	for _, want := range []string{
		"capabilities contains duplicate value interactive_session",
		"surface.target.component_id must match component id",
		"default_target.component_id must match component id",
		"action.operation_name contains invalid value Mirror Message",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("validation error %q does not contain %q", message, want)
		}
	}
}

func TestDiscoverComponentStatusesMarksMissingDocsInvalid(t *testing.T) {
	root := t.TempDir()
	componentRoot := filepath.Join(root, "modules", "probe")
	if err := os.MkdirAll(componentRoot, 0o755); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	writeFile(t, filepath.Join(componentRoot, ComponentManifestFile), `{
  "id": "probe",
  "name": "Probe",
  "version": "0.1.0",
  "stage": "prototype",
  "release_line": "component-probe",
  "release_channel": "public_preview",
  "shell_contract": "v2",
  "component_class": "heavy",
  "runtime_shape": "worker",
  "runtime_owner": "watcher-service",
  "shell_dependencies": ["event_bus", "diagnostics", "worker_lane"],
  "docs": ["modules/probe/README.md"],
  "worker": {
    "entrypoint": "python3",
    "healthcheck": "process_and_ping",
    "operations": ["job.run"],
    "streams": ["probe.job"]
  }
}`)
	statuses, err := DiscoverComponentStatuses(filepath.Join(root, "modules"), model.ShellContractV2)
	if err != nil {
		t.Fatalf("DiscoverComponentStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].DocsPresent {
		t.Fatalf("expected docs_present=false: %+v", statuses[0])
	}
	if statuses[0].ManifestValid {
		t.Fatalf("expected manifest to be invalid when docs are missing: %+v", statuses[0])
	}
}

func TestDiscoverComponentStatusesDisablesArchivedComponents(t *testing.T) {
	root := t.TempDir()
	componentRoot := filepath.Join(root, "modules", "pilot")
	if err := os.MkdirAll(componentRoot, 0o755); err != nil {
		t.Fatalf("mkdir pilot: %v", err)
	}
	writeFile(t, filepath.Join(componentRoot, "README.md"), "# Pilot\n")
	writeFile(t, filepath.Join(componentRoot, ComponentManifestFile), `{
  "id": "pilot",
  "name": "Pilot",
  "version": "0.1.0",
  "stage": "archived",
  "release_line": "archive-pilot",
  "release_channel": "archived",
  "shell_contract": "v2",
  "component_class": "heavy",
  "runtime_shape": "worker",
  "runtime_owner": "watcher-service",
  "shell_dependencies": ["event_bus", "operation_contract", "diagnostics", "worker_lane"],
  "docs": ["modules/pilot/README.md"],
  "worker": {
    "entrypoint": "python3",
    "healthcheck": "process_and_ping",
    "operations": ["brief.create"],
    "streams": ["pilot.suggestion"]
  }
}`)

	statuses, err := DiscoverComponentStatuses(filepath.Join(root, "modules"), model.ShellContractV2)
	if err != nil {
		t.Fatalf("DiscoverComponentStatuses: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	status := statuses[0]
	if !status.ManifestValid || !status.DocsPresent || !status.ShellContractCompatible {
		t.Fatalf("archived manifest should remain valid reference material: %+v", status)
	}
	if status.RuntimeEnabled || status.RuntimeStatus != model.RuntimeStatusArchived {
		t.Fatalf("archived component runtime = enabled:%v status:%q, want disabled archived", status.RuntimeEnabled, status.RuntimeStatus)
	}
}

func TestApplyRuntimeDiagnosticsDoesNotReviveArchivedComponents(t *testing.T) {
	statuses := []model.ComponentStatus{{
		Manifest: model.ComponentManifest{
			ID:             "codex",
			Stage:          "archived",
			ReleaseChannel: "archived",
		},
		RuntimeEnabled: false,
		RuntimeStatus:  model.RuntimeStatusArchived,
	}}
	got := ApplyRuntimeDiagnostics(statuses, map[string]model.ComponentRuntimeDiagnostics{
		"codex": {Enabled: true, Status: model.RuntimeStatusReady},
	})
	if got[0].RuntimeEnabled || got[0].RuntimeStatus != model.RuntimeStatusArchived {
		t.Fatalf("archived component revived by diagnostics: %+v", got[0])
	}
}

func TestRepositoryComponentManifestsPassValidation(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	shell, err := LoadShellStatus(
		filepath.Join(repoRoot, "watcher.shell.json"),
		filepath.Join(repoRoot, "VERSION"),
		filepath.Join(repoRoot, "modules"),
	)
	if err != nil {
		t.Fatalf("LoadShellStatus(repo): %v", err)
	}
	statuses, err := DiscoverComponentStatuses(filepath.Join(repoRoot, "modules"), shell.Manifest.ContractVersion)
	if err != nil {
		t.Fatalf("DiscoverComponentStatuses(repo): %v", err)
	}
	if err := ValidateComponentStatuses(statuses); err != nil {
		t.Fatalf("ValidateComponentStatuses(repo): %v", err)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
