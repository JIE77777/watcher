package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"watcher/internal/model"
	"watcher/internal/netpolicy"
)

type ToolManifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Kind        string `json:"kind"`
	Language    string `json:"language"`
	Runtime     string `json:"runtime,omitempty"`
	EntryPoint  string `json:"entry_point"`
	Description string `json:"description,omitempty"`
}

type ToolRunner struct {
	Root string
}

func DiscoverTools(root string) ([]ToolManifest, error) {
	var manifests []ToolManifest
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "manifest.json" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var manifest ToolManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		if err := ValidateToolManifest(manifest); err != nil {
			return fmt.Errorf("manifest %s: %w", path, err)
		}
		if !filepath.IsAbs(manifest.EntryPoint) {
			manifest.EntryPoint = filepath.Join(filepath.Dir(path), manifest.EntryPoint)
		}
		manifests = append(manifests, manifest)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].ID < manifests[j].ID
	})
	return manifests, nil
}

func ValidateToolManifest(manifest ToolManifest) error {
	if strings.TrimSpace(manifest.ID) == "" {
		return errors.New("id is required")
	}
	if !isStableToolID(manifest.ID) {
		return fmt.Errorf("id %q must use lowercase ascii letters, numbers, '-' or '_'", manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return errors.New("version is required")
	}
	switch strings.TrimSpace(manifest.Kind) {
	case "scraper", "connector", "parser":
	default:
		return fmt.Errorf("kind %q must be scraper, connector, or parser", manifest.Kind)
	}
	if strings.TrimSpace(manifest.Language) == "" {
		return errors.New("language is required")
	}
	if strings.TrimSpace(manifest.EntryPoint) == "" {
		return errors.New("entry_point is required")
	}
	return nil
}

func isStableToolID(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_':
		default:
			return false
		}
	}
	return true
}

func IndexByID(manifests []ToolManifest) map[string]ToolManifest {
	index := make(map[string]ToolManifest, len(manifests))
	for _, manifest := range manifests {
		index[manifest.ID] = manifest
	}
	return index
}

func (r ToolRunner) Run(ctx context.Context, task model.WatchTask, manifest ToolManifest) (model.SourceSnapshot, string, error) {
	settings, err := model.ParseTaskSettings(task.Settings)
	if err != nil {
		return model.SourceSnapshot{}, "", fmt.Errorf("parse task settings: %w", err)
	}

	toolConfig := make(map[string]any)
	if len(settings.ToolConfig) > 0 {
		if err := json.Unmarshal(settings.ToolConfig, &toolConfig); err != nil {
			return model.SourceSnapshot{}, "", fmt.Errorf("parse tool config: %w", err)
		}
	}
	toolConfig["task_id"] = task.ID
	toolConfig["task_name"] = task.Name
	toolConfig["task_labels"] = task.Labels

	configFile, err := os.CreateTemp("", "watcher-tool-config-*.json")
	if err != nil {
		return model.SourceSnapshot{}, "", fmt.Errorf("create temp config: %w", err)
	}
	defer os.Remove(configFile.Name())

	configBytes, err := json.MarshalIndent(toolConfig, "", "  ")
	if err != nil {
		return model.SourceSnapshot{}, "", fmt.Errorf("encode tool config: %w", err)
	}
	if _, err := configFile.Write(configBytes); err != nil {
		return model.SourceSnapshot{}, "", fmt.Errorf("write temp config: %w", err)
	}
	if err := configFile.Close(); err != nil {
		return model.SourceSnapshot{}, "", fmt.Errorf("close temp config: %w", err)
	}

	commandName := manifest.Runtime
	if commandName == "" {
		switch strings.ToLower(manifest.Language) {
		case "python":
			commandName = "python3"
		default:
			return model.SourceSnapshot{}, "", fmt.Errorf("unsupported tool language %q", manifest.Language)
		}
	}

	cmd := exec.CommandContext(ctx, commandName, manifest.EntryPoint, "--config", configFile.Name())
	cmd.Dir = filepath.Dir(manifest.EntryPoint)
	cmd.Env = netpolicy.CurrentEnvWithoutProxy()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return model.SourceSnapshot{}, combinedToolOutput(output, stderr.Bytes()), fmt.Errorf("run %s: %w", manifest.ID, err)
	}

	var snapshot model.SourceSnapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return model.SourceSnapshot{}, combinedToolOutput(output, stderr.Bytes()), fmt.Errorf("decode tool output: %w", err)
	}

	if err := validateSnapshot(&snapshot, task, manifest); err != nil {
		return model.SourceSnapshot{}, combinedToolOutput(output, stderr.Bytes()), err
	}
	return snapshot, combinedToolOutput(output, stderr.Bytes()), nil
}

func validateSnapshot(snapshot *model.SourceSnapshot, task model.WatchTask, manifest ToolManifest) error {
	if snapshot.SourceID == "" {
		snapshot.SourceID = manifest.ID
	}
	if snapshot.SourceID != manifest.ID {
		return fmt.Errorf("tool returned mismatched source_id %q", snapshot.SourceID)
	}
	if snapshot.TaskID == "" {
		snapshot.TaskID = task.ID
	}
	if snapshot.FetchedAt == "" {
		snapshot.FetchedAt = model.NowString()
	}
	if snapshot.Version == "" {
		snapshot.Version = manifest.Version
	}
	if snapshot.TaskID != task.ID {
		return errors.New("tool returned mismatched task_id")
	}
	for idx := range snapshot.Items {
		item := &snapshot.Items[idx]
		if item.ItemKey == "" {
			return fmt.Errorf("snapshot item %d is missing item_key", idx)
		}
		if item.ThreadKey == "" {
			item.ThreadKey = task.ID + ":" + item.ItemKey
		}
		if item.Title == "" {
			item.Title = item.ItemKey
		}
	}
	return nil
}

func combinedToolOutput(stdout []byte, stderr []byte) string {
	stdoutText := strings.TrimSpace(string(stdout))
	stderrText := strings.TrimSpace(string(stderr))
	switch {
	case stdoutText == "":
		return stderrText
	case stderrText == "":
		return stdoutText
	default:
		return stdoutText + "\n" + stderrText
	}
}
