package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"watcher/internal/model"
)

func main() {
	repoRoot := flag.String("repo-root", ".", "Watcher repository root")
	componentID := flag.String("id", "", "Component identifier")
	name := flag.String("name", "", "Component display name")
	class := flag.String("class", model.ComponentClassLight, "Component class: light or heavy")
	stage := flag.String("stage", "draft", "Component stage")
	flag.Parse()

	if strings.TrimSpace(*componentID) == "" || strings.TrimSpace(*name) == "" {
		fail("id and name are required")
	}
	root, err := filepath.Abs(*repoRoot)
	if err != nil {
		fail(err.Error())
	}

	manifest, docsPath, testPlanPath := buildManifest(*componentID, *name, *class, *stage)
	componentRoot := filepath.Join(root, "modules", manifest.ID)
	if err := os.MkdirAll(componentRoot, 0o755); err != nil {
		fail(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, docsPath)), 0o755); err != nil {
		fail(err.Error())
	}

	writeJSON(filepath.Join(componentRoot, "component.json"), manifest)
	writeText(filepath.Join(componentRoot, "README.md"), componentReadme(manifest))
	writeText(filepath.Join(root, docsPath), componentDoc(manifest))
	writeText(filepath.Join(root, testPlanPath), componentTestPlan(manifest, docsPath))
}

func buildManifest(id, name, class, stage string) (model.ComponentManifest, string, string) {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	class = strings.TrimSpace(class)
	stage = strings.TrimSpace(stage)

	docName := strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_COMPONENT.md"
	docsPath := filepath.Join("docs", "modules", docName)
	testPlanPath := filepath.Join("modules", id, "TEST_PLAN.md")

	manifest := model.ComponentManifest{
		ID:                id,
		Name:              name,
		Version:           "0.1.0",
		Stage:             stage,
		ReleaseLine:       "component-" + id,
		ReleaseChannel:    "public_preview",
		ShellContract:     model.ShellContractV2,
		ComponentClass:    class,
		RuntimeShape:      model.RuntimeShapeInProcess,
		RuntimeOwner:      "watcher-service",
		Capabilities:      []string{"operation", "health"},
		ShellDependencies: []string{"owner_auth", "event_bus", "operation_contract", "app_sync"},
		Docs:              []string{filepath.Join("modules", id, "README.md"), docsPath},
		NonGoals:          []string{"custom_transport", "custom_sync_protocol"},
	}
	if class == model.ComponentClassHeavy {
		manifest.RuntimeShape = model.RuntimeShapeWorker
		manifest.Capabilities = []string{"operation", "worker_runtime", "health"}
		manifest.Resources = []string{"job"}
		manifest.Operations = []string{"job.run"}
		manifest.Streams = []string{id + ".job"}
		manifest.Worker = &model.ComponentWorkerConfig{
			Entrypoint:  "python3",
			Args:        []string{filepath.Join("modules", id, "worker.py")},
			Env:         map[string]string{"PYTHONUNBUFFERED": "1"},
			Healthcheck: "process_and_ping",
			Operations:  append([]string(nil), manifest.Operations...),
			Streams:     append([]string(nil), manifest.Streams...),
		}
	} else {
		manifest.Resources = []string{"resource"}
		manifest.Operations = []string{"resource.run"}
		manifest.Streams = []string{id + ".resource"}
	}
	manifest.Surfaces = []model.ModuleSurface{{
		ID:      "operations",
		Title:   name + " Operations",
		Kind:    "operation_list",
		Primary: true,
		Target:  model.ShellTarget{ComponentID: id, Surface: "operations"},
	}}
	manifest.DefaultTarget = &model.ShellTarget{ComponentID: id, Surface: "operations"}
	manifest.Actions = []model.ModuleAction{{
		ActionID:      manifest.Operations[0],
		Label:         "Run " + name,
		Kind:          "run",
		OperationName: manifest.Operations[0],
		Async:         true,
		Target:        &model.ShellTarget{ComponentID: id, Surface: "operations"},
	}}
	return manifest, docsPath, testPlanPath
}

func componentReadme(manifest model.ComponentManifest) string {
	return fmt.Sprintf(`# %s Component

`+"`%s`"+` 是 `+"`watcher shell`"+` 上的组件。

- stage: %s
- class: %s
- runtime: %s
- capabilities: %s
- surfaces: %s
- actions: %s
- resources: %s
- operations: %s
- streams: %s

## Shell Dependencies

%s

## Non Goals

%s
`,
		manifest.Name,
		manifest.ID,
		manifest.Stage,
		manifest.ComponentClass,
		manifest.RuntimeShape,
		strings.Join(manifest.Capabilities, ", "),
		moduleSurfaceList(manifest.Surfaces),
		moduleActionList(manifest.Actions),
		strings.Join(manifest.Resources, ", "),
		strings.Join(manifest.Operations, ", "),
		strings.Join(manifest.Streams, ", "),
		bulletList(manifest.ShellDependencies),
		bulletList(manifest.NonGoals),
	)
}

func componentDoc(manifest model.ComponentManifest) string {
	return fmt.Sprintf(`# %s Component

## Scope

- fill in the domain model
- fill in the user-facing value

## Capabilities

%s

## Surfaces

%s

## Actions

%s

## Resources

%s

## Operations

%s

## Streams

%s

## Non Goals

%s
`,
		manifest.Name,
		bulletList(manifest.Capabilities),
		bulletList(moduleSurfaceIDs(manifest.Surfaces)),
		bulletList(moduleActionIDs(manifest.Actions)),
		bulletList(manifest.Resources),
		bulletList(manifest.Operations),
		bulletList(manifest.Streams),
		bulletList(manifest.NonGoals),
	)
}

func componentTestPlan(manifest model.ComponentManifest, docsPath string) string {
	return fmt.Sprintf(`# %s Test Plan

- manifest passes shell contract %s validation
- manifest declares capabilities, surfaces, default_target, and actions
- README and docs exist
- component resources can be read through public v2 API
- component operations run through async lifecycle
- component events arrive through typed event bus
- docs reference: %s
`,
		manifest.Name,
		model.ShellContractV2,
		docsPath,
	)
}

func bulletList(items []string) string {
	if len(items) == 0 {
		return "- none"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func moduleSurfaceIDs(items []model.ModuleSurface) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.ID+" ("+item.Kind+")")
	}
	return values
}

func moduleActionIDs(items []model.ModuleAction) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, item.ActionID+" ("+item.Kind+")")
	}
	return values
}

func moduleSurfaceList(items []model.ModuleSurface) string {
	return strings.Join(moduleSurfaceIDs(items), ", ")
}

func moduleActionList(items []model.ModuleAction) string {
	return strings.Join(moduleActionIDs(items), ", ")
}

func writeJSON(path string, value any) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail(err.Error())
	}
	writeText(path, string(payload)+"\n")
}

func writeText(path string, value string) {
	if _, err := os.Stat(path); err == nil {
		fail("refusing to overwrite existing file: " + path)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
