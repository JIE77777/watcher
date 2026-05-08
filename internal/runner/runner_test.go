package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"watcher/internal/model"
)

func TestRunToolSuccess(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "run.py")
	manifestPath := filepath.Join(root, "manifest.json")

	writeFile(t, scriptPath, `#!/usr/bin/env python3
import json
import sys
config_path = sys.argv[sys.argv.index("--config") + 1]
config = json.load(open(config_path, "r", encoding="utf-8"))
print(json.dumps({
  "source_id": "fixture",
  "task_id": config["task_id"],
  "fetched_at": "2026-04-23T09:16:24Z",
  "version": "v1",
  "items": [{
    "item_key": "demo",
    "thread_key": "fixture:demo",
    "title": "Demo",
    "data": {"score": 1}
  }]
}))
`)
	writeFile(t, manifestPath, `{
  "id": "fixture",
  "name": "Fixture",
  "version": "v1",
  "kind": "scraper",
  "language": "python",
  "runtime": "python3",
  "entry_point": "run.py"
}`)
	_ = os.Chmod(scriptPath, 0o755)

	manifests, err := DiscoverTools(root)
	if err != nil {
		t.Fatalf("DiscoverTools() error = %v", err)
	}
	manifest := manifests[0]

	task := model.WatchTask{
		ID:   "task_demo",
		Name: "Fixture task",
		Tool: "fixture",
		Settings: mustJSON(t, model.TaskSettings{
			ToolConfig: json.RawMessage(`{"hello":"world"}`),
		}),
	}

	snapshot, _, err := (ToolRunner{Root: root}).Run(context.Background(), task, manifest)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if snapshot.TaskID != "task_demo" {
		t.Fatalf("expected task id to round-trip, got %q", snapshot.TaskID)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ItemKey != "demo" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestRunToolAllowsStderrDiagnosticsOnSuccess(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "run.py")
	manifestPath := filepath.Join(root, "manifest.json")

	writeFile(t, scriptPath, `#!/usr/bin/env python3
import json
import sys
config_path = sys.argv[sys.argv.index("--config") + 1]
config = json.load(open(config_path, "r", encoding="utf-8"))
print("[tool-info] fetched fixture", file=sys.stderr)
print(json.dumps({
  "source_id": "fixture",
  "task_id": config["task_id"],
  "fetched_at": "2026-04-23T09:16:24Z",
  "version": "v1",
  "items": [{"item_key": "demo", "thread_key": "fixture:demo", "title": "Demo"}]
}))
`)
	writeFile(t, manifestPath, `{
  "id": "fixture",
  "name": "Fixture",
  "version": "v1",
  "kind": "scraper",
  "language": "python",
  "runtime": "python3",
  "entry_point": "run.py"
}`)
	_ = os.Chmod(scriptPath, 0o755)

	manifests, err := DiscoverTools(root)
	if err != nil {
		t.Fatalf("DiscoverTools() error = %v", err)
	}
	task := model.WatchTask{ID: "task_demo", Name: "Fixture task", Tool: "fixture"}
	snapshot, output, err := (ToolRunner{Root: root}).Run(context.Background(), task, manifests[0])
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if snapshot.SourceID != "fixture" {
		t.Fatalf("SourceID = %q, want fixture", snapshot.SourceID)
	}
	if !strings.Contains(output, "[tool-info] fetched fixture") {
		t.Fatalf("expected stderr diagnostics in output, got %q", output)
	}
}

func TestRunToolRejectsInvalidJSON(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "run.py")
	manifestPath := filepath.Join(root, "manifest.json")

	writeFile(t, scriptPath, `#!/usr/bin/env python3
print("not json")
`)
	writeFile(t, manifestPath, `{
  "id": "fixture_bad",
  "name": "Fixture Bad",
  "version": "v1",
  "kind": "scraper",
  "language": "python",
  "runtime": "python3",
  "entry_point": "run.py"
}`)
	_ = os.Chmod(scriptPath, 0o755)

	manifest, err := DiscoverTools(root)
	if err != nil {
		t.Fatalf("DiscoverTools() error = %v", err)
	}

	task := model.WatchTask{ID: "task_bad", Name: "Bad", Tool: "fixture_bad"}
	if _, _, err := (ToolRunner{Root: root}).Run(context.Background(), task, manifest[0]); err == nil {
		t.Fatalf("expected invalid JSON error")
	}
}

func TestDiscoverToolsRejectsWeakManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	writeFile(t, manifestPath, `{
  "id": "Bad Tool",
  "name": "Bad",
  "version": "v1",
  "kind": "scraper",
  "language": "python",
  "entry_point": "run.py"
}`)
	if _, err := DiscoverTools(root); err == nil {
		t.Fatalf("expected weak manifest to be rejected")
	}
}

func TestRunToolRejectsMismatchedSourceID(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "run.py")
	manifestPath := filepath.Join(root, "manifest.json")
	writeFile(t, scriptPath, `#!/usr/bin/env python3
import json
print(json.dumps({
  "source_id": "other",
  "task_id": "task_demo",
  "fetched_at": "2026-04-23T09:16:24Z",
  "version": "v1",
  "items": [{"item_key": "demo", "thread_key": "fixture:demo", "title": "Demo"}]
}))
`)
	writeFile(t, manifestPath, `{
  "id": "fixture",
  "name": "Fixture",
  "version": "v1",
  "kind": "scraper",
  "language": "python",
  "runtime": "python3",
  "entry_point": "run.py"
}`)
	_ = os.Chmod(scriptPath, 0o755)
	manifests, err := DiscoverTools(root)
	if err != nil {
		t.Fatalf("DiscoverTools() error = %v", err)
	}
	task := model.WatchTask{ID: "task_demo", Name: "Fixture task", Tool: "fixture"}
	if _, _, err := (ToolRunner{Root: root}).Run(context.Background(), task, manifests[0]); err == nil {
		t.Fatalf("expected mismatched source_id to be rejected")
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
