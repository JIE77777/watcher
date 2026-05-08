package main

import (
	"encoding/json"
	"strings"
	"testing"

	"watcher/internal/model"
)

func TestCCMimoDefaultsAreFullAccess(t *testing.T) {
	mode, err := normalizeCCMimoPermissionMode("")
	if err != nil {
		t.Fatalf("normalize empty permission mode: %v", err)
	}
	if mode != "bypassPermissions" {
		t.Fatalf("empty permission mode = %q, want bypassPermissions", mode)
	}

	mode, err = normalizeCCMimoPermissionMode("plan")
	if err != nil {
		t.Fatalf("normalize legacy plan permission mode: %v", err)
	}
	if mode != "bypassPermissions" {
		t.Fatalf("legacy plan permission mode = %q, want bypassPermissions", mode)
	}

	tools := normalizeCCMimoAllowedTools([]string{"Read", "LS", "Grep", "Glob"})
	if len(tools) != 1 || tools[0] != "default" {
		t.Fatalf("legacy read-only tools = %#v, want [default]", tools)
	}
}

func TestCCMimoTimeoutNormalizesMobileDefault(t *testing.T) {
	if got := normalizeCCMimoTimeoutSeconds(0); got != defaultCCMimoTimeoutSeconds {
		t.Fatalf("empty timeout = %d, want %d", got, defaultCCMimoTimeoutSeconds)
	}
	if got := normalizeCCMimoTimeoutSeconds(300); got != defaultCCMimoTimeoutSeconds {
		t.Fatalf("mobile timeout = %d, want %d", got, defaultCCMimoTimeoutSeconds)
	}
	if got := normalizeCCMimoTimeoutSeconds(maxCCMimoTimeoutSeconds + 1); got != maxCCMimoTimeoutSeconds {
		t.Fatalf("large timeout = %d, want %d", got, maxCCMimoTimeoutSeconds)
	}
}

func TestCCMimoWorkflowIsWorktreePatchOnly(t *testing.T) {
	workflow, err := normalizeCCMimoWorkflow("")
	if err != nil {
		t.Fatalf("normalize empty workflow: %v", err)
	}
	if workflow != ccMimoWorkflowWorktreePatch {
		t.Fatalf("empty workflow = %q, want %q", workflow, ccMimoWorkflowWorktreePatch)
	}
	if _, err := normalizeCCMimoWorkflow("direct"); err == nil {
		t.Fatal("direct workflow should not be accepted in the product API")
	}
}

func TestCCMimoCapsuleIsCompact(t *testing.T) {
	capsule := ccMimoCapsule(
		model.ShellStatus{
			Version:       "0.1.0",
			ServiceStatus: "running",
			Manifest: model.ShellManifest{
				ID:              "watcher.shell",
				ContractVersion: "v2",
			},
			ComponentStats: model.ComponentStats{Total: 1, Running: 1},
		},
		[]model.ComponentStatus{{
			Manifest: model.ComponentManifest{
				ID:              "codex",
				Name:            "Codex",
				ComponentClass:  "light",
				RuntimeShape:    "in_process",
				Operations:      []string{"turn.start"},
				Streams:         []string{"codex.operation"},
				Resources:       []string{"thread"},
				RuntimeOwner:    "watcher-service",
				ShellContract:   "v2",
				ReleaseLine:     "component-codex",
				ReleaseChannel:  "public_preview",
				Stage:           "active",
				Version:         "0.1.0",
				AndroidSurfaces: []string{"codex_thread"},
			},
			Enabled:            true,
			RuntimeStatus:      "running",
			RuntimeDetails:     map[string]string{"recent_stderr": strings.Repeat("x", 4000)},
			InflightOperations: 1,
		}},
		[]model.ShellDiagnosticEvent{{
			ComponentID: "codex",
			Kind:        "runtime.stderr",
			Severity:    "warning",
			Message:     strings.Repeat("y", 800),
			OccurredAt:  "2026-04-25T09:00:00Z",
		}},
	)
	raw, err := json.Marshal(capsule)
	if err != nil {
		t.Fatalf("marshal capsule: %v", err)
	}
	if strings.Contains(string(raw), "recent_stderr") {
		t.Fatalf("capsule leaked runtime details: %s", string(raw))
	}
	if len(raw) > 3000 {
		t.Fatalf("capsule too large: %d bytes", len(raw))
	}
}
