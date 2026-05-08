package main

import (
	"testing"
	"time"

	"watcher/internal/model"
)

func TestFilterAndSortShellSignals(t *testing.T) {
	now := time.Now().UTC()
	signals := []model.ShellSignal{
		{
			SignalID:   "old",
			Title:      "old",
			OccurredAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
		},
		{
			SignalID:       "action",
			Title:          "action",
			OccurredAt:     now.Add(-3 * time.Hour).Format(time.RFC3339),
			ActionRequired: true,
		},
		{
			SignalID:  "expired",
			Title:     "expired",
			ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339),
		},
		{
			SignalID:   "new",
			Title:      "new",
			OccurredAt: now.Add(-time.Minute).Format(time.RFC3339),
		},
		{
			SignalID:   "",
			Title:      "ignored",
			OccurredAt: now.Format(time.RFC3339),
		},
	}

	filtered := filterAndSortShellSignals(signals, 3)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(filtered))
	}
	if filtered[0].SignalID != "action" {
		t.Fatalf("action-required signal should sort first, got %q", filtered[0].SignalID)
	}
	if filtered[1].SignalID != "new" || filtered[2].SignalID != "old" {
		t.Fatalf("signals not sorted by occurred_at desc after action signals: %#v", filtered)
	}
}

func TestShellHomeArrayHelpersNeverReturnNil(t *testing.T) {
	if nonNilSignals(nil) == nil {
		t.Fatal("signals helper returned nil")
	}
	if nonNilCells(nil) == nil {
		t.Fatal("cells helper returned nil")
	}
}

func TestComponentCellUsesManifestDefaultTarget(t *testing.T) {
	target := model.ShellTarget{ComponentID: "opencode", Surface: "sessions"}
	cell := (&App{}).componentCell(
		"opencode",
		"Opencode",
		"o",
		model.ComponentStatus{
			Manifest: model.ComponentManifest{
				ID:            "opencode",
				Name:          "Opencode",
				DefaultTarget: &target,
			},
			RuntimeStatus: model.RuntimeStatusReady,
		},
		model.ShellTarget{ComponentID: "opencode", Surface: "legacy"},
	)
	if cell.Target.Surface != "sessions" {
		t.Fatalf("cell target surface = %q, want sessions", cell.Target.Surface)
	}
}

func TestShellHomeComponentCellsUseManifestEntries(t *testing.T) {
	opencodeTarget := model.ShellTarget{ComponentID: "opencode", Surface: "sessions"}
	probeTarget := model.ShellTarget{ComponentID: "probe", Surface: "operations"}
	cells := (&App{}).shellHomeComponentCells([]model.ComponentStatus{
		{
			Manifest: model.ComponentManifest{
				ID:              "probe",
				Name:            "Probe",
				DefaultTarget:   &probeTarget,
				AndroidSurfaces: []string{},
			},
			RuntimeStatus: model.RuntimeStatusReady,
		},
		{
			Manifest: model.ComponentManifest{
				ID:              "opencode",
				Name:            "Opencode",
				DefaultTarget:   &opencodeTarget,
				AndroidSurfaces: []string{"opencode_sessions"},
			},
			RuntimeEnabled: true,
			RuntimeStatus:  model.RuntimeStatusReady,
			ManifestValid:  true,
		},
	})

	if len(cells) != 2 {
		t.Fatalf("len(cells) = %d, want opencode plus game", len(cells))
	}
	if cells[0].ComponentID != "opencode" || cells[0].Target.Surface != "sessions" {
		t.Fatalf("unexpected module cell: %#v", cells[0])
	}
	if cells[1].ComponentID != "game" {
		t.Fatalf("local game cell should remain last, got %#v", cells[1])
	}
}

func TestShellHomeComponentCellsHideArchivedModules(t *testing.T) {
	cells := (&App{}).shellHomeComponentCells([]model.ComponentStatus{
		{
			Manifest: model.ComponentManifest{
				ID:              "codex",
				Name:            "Codex",
				Stage:           "archived",
				ReleaseChannel:  "archived",
				AndroidSurfaces: []string{"codex_sessions"},
			},
			RuntimeEnabled: false,
			RuntimeStatus:  model.RuntimeStatusArchived,
			ManifestValid:  true,
		},
	})

	if len(cells) != 1 || cells[0].ComponentID != "game" {
		t.Fatalf("archived module should be hidden from shell home tools, got %#v", cells)
	}
}
