package components

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"watcher/internal/model"
)

const ComponentManifestFile = "component.json"

func LoadShellStatus(manifestPath, versionFile, componentsRoot string) (model.ShellStatus, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return model.ShellStatus{}, err
	}
	var manifest model.ShellManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return model.ShellStatus{}, fmt.Errorf("parse shell manifest: %w", err)
	}
	if err := validateShellManifest(manifestPath, manifest); err != nil {
		return model.ShellStatus{}, err
	}
	versionBytes, err := os.ReadFile(versionFile)
	if err != nil {
		return model.ShellStatus{}, err
	}
	version := strings.TrimSpace(string(versionBytes))
	if version == "" {
		return model.ShellStatus{}, fmt.Errorf("shell version file %s is empty", versionFile)
	}
	return model.ShellStatus{
		Manifest:       manifest,
		Version:        version,
		ManifestPath:   manifestPath,
		VersionFile:    versionFile,
		ComponentsRoot: componentsRoot,
	}, nil
}

func DiscoverComponentStatuses(root string, shellContract string) ([]model.ComponentStatus, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(root)

	var statuses []model.ComponentStatus
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(root, entry.Name(), ComponentManifestFile)
		if _, err := os.Stat(manifestPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		status := model.ComponentStatus{
			ManifestPath:  manifestPath,
			Enabled:       true,
			ManifestValid: false,
			RuntimeStatus: model.RuntimeStatusInvalid,
		}

		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &status.Manifest); err != nil {
			status.ValidationError = fmt.Sprintf("parse component manifest %s: %v", manifestPath, err)
			statuses = append(statuses, status)
			continue
		}

		if err := ValidateComponentManifest(status.Manifest); err != nil {
			status.ValidationError = err.Error()
		} else {
			status.DocsPresent = docsPresent(repoRoot, status.Manifest.Docs)
			if !status.DocsPresent {
				status.ValidationError = fmt.Sprintf("component manifest %s references missing docs", manifestPath)
			} else {
				status.ManifestValid = true
			}
		}
		status.ShellContractCompatible = status.ManifestValid && status.Manifest.ShellContract == shellContract
		archived := IsArchivedComponent(status.Manifest)
		status.RuntimeEnabled = status.ManifestValid && status.ShellContractCompatible && !archived
		switch status.Manifest.RuntimeShape {
		case model.RuntimeShapeInProcess:
			if archived {
				status.RuntimeStatus = model.RuntimeStatusArchived
			} else if status.RuntimeEnabled {
				status.RuntimeStatus = model.RuntimeStatusReady
			}
		case model.RuntimeShapeWorker:
			if archived {
				status.RuntimeStatus = model.RuntimeStatusArchived
			} else if status.RuntimeEnabled {
				status.RuntimeStatus = model.RuntimeStatusStopped
			}
		}
		if status.ManifestValid && !status.ShellContractCompatible {
			status.ValidationError = fmt.Sprintf(
				"component manifest %s expects shell_contract=%s, shell is %s",
				manifestPath,
				status.Manifest.ShellContract,
				shellContract,
			)
		}
		statuses = append(statuses, status)
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Manifest.ID < statuses[j].Manifest.ID
	})
	return statuses, nil
}

func IsArchivedComponent(manifest model.ComponentManifest) bool {
	stage := strings.ToLower(strings.TrimSpace(manifest.Stage))
	channel := strings.ToLower(strings.TrimSpace(manifest.ReleaseChannel))
	return stage == "archived" || channel == "archived"
}

func ValidateComponentManifest(manifest model.ComponentManifest) error {
	var problems []string
	require := func(fieldName, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fieldName+" is required")
		}
	}

	require("id", manifest.ID)
	require("name", manifest.Name)
	require("version", manifest.Version)
	require("stage", manifest.Stage)
	require("release_line", manifest.ReleaseLine)
	require("release_channel", manifest.ReleaseChannel)
	require("shell_contract", manifest.ShellContract)
	require("component_class", manifest.ComponentClass)
	require("runtime_shape", manifest.RuntimeShape)
	require("runtime_owner", manifest.RuntimeOwner)
	if len(manifest.Docs) == 0 {
		problems = append(problems, "docs must not be empty")
	}
	if len(manifest.ShellDependencies) == 0 {
		problems = append(problems, "shell_dependencies must not be empty")
	}
	validateStableList("capabilities", manifest.Capabilities, &problems)
	validateModuleSurfaces(manifest, &problems)
	validateModuleDefaultTarget(manifest, &problems)
	validateModuleActions(manifest, &problems)

	switch manifest.ComponentClass {
	case model.ComponentClassLight:
		if manifest.RuntimeShape != model.RuntimeShapeInProcess {
			problems = append(problems, "light components must use runtime_shape=in_process")
		}
	case model.ComponentClassHeavy:
		if manifest.RuntimeShape != model.RuntimeShapeWorker {
			problems = append(problems, "heavy components must use runtime_shape=worker")
		}
	default:
		problems = append(problems, "component_class must be light or heavy")
	}

	switch manifest.RuntimeShape {
	case model.RuntimeShapeInProcess:
		if manifest.Worker != nil {
			problems = append(problems, "worker block is only allowed for runtime_shape=worker")
		}
	case model.RuntimeShapeWorker:
		if manifest.Worker == nil {
			problems = append(problems, "worker block is required for runtime_shape=worker")
		} else {
			if strings.TrimSpace(manifest.Worker.Entrypoint) == "" {
				problems = append(problems, "worker.entrypoint is required")
			}
			if strings.TrimSpace(manifest.Worker.Healthcheck) == "" {
				problems = append(problems, "worker.healthcheck is required")
			}
			if len(manifest.Worker.Operations) == 0 {
				problems = append(problems, "worker.operations must not be empty")
			}
			if len(manifest.Worker.Streams) == 0 {
				problems = append(problems, "worker.streams must not be empty")
			}
		}
	default:
		problems = append(problems, "runtime_shape must be in_process or worker")
	}

	if len(problems) > 0 {
		return fmt.Errorf("component manifest %s: %s", manifest.ID, strings.Join(problems, "; "))
	}
	return nil
}

func validateStableList(fieldName string, values []string, problems *[]string) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			*problems = append(*problems, fieldName+" must not contain empty values")
			continue
		}
		if !isStableToken(cleaned) {
			*problems = append(*problems, fieldName+" contains invalid value "+cleaned)
		}
		if _, ok := seen[cleaned]; ok {
			*problems = append(*problems, fieldName+" contains duplicate value "+cleaned)
		}
		seen[cleaned] = struct{}{}
	}
}

func validateModuleSurfaces(manifest model.ComponentManifest, problems *[]string) {
	seen := make(map[string]struct{}, len(manifest.Surfaces))
	for _, surface := range manifest.Surfaces {
		id := strings.TrimSpace(surface.ID)
		if id == "" {
			*problems = append(*problems, "surface.id is required")
		} else {
			if !isStableToken(id) {
				*problems = append(*problems, "surface.id contains invalid value "+id)
			}
			if _, ok := seen[id]; ok {
				*problems = append(*problems, "surfaces contains duplicate id "+id)
			}
			seen[id] = struct{}{}
		}
		if strings.TrimSpace(surface.Kind) == "" {
			*problems = append(*problems, "surface.kind is required")
		} else if !isStableToken(surface.Kind) {
			*problems = append(*problems, "surface.kind contains invalid value "+surface.Kind)
		}
		if strings.TrimSpace(surface.Target.ComponentID) == "" {
			*problems = append(*problems, "surface.target.component_id is required")
		} else if surface.Target.ComponentID != manifest.ID {
			*problems = append(*problems, "surface.target.component_id must match component id")
		}
		if strings.TrimSpace(surface.Target.Surface) == "" {
			*problems = append(*problems, "surface.target.surface is required")
		}
	}
}

func validateModuleDefaultTarget(manifest model.ComponentManifest, problems *[]string) {
	if manifest.DefaultTarget == nil {
		return
	}
	if strings.TrimSpace(manifest.DefaultTarget.ComponentID) == "" {
		*problems = append(*problems, "default_target.component_id is required")
	} else if manifest.DefaultTarget.ComponentID != manifest.ID {
		*problems = append(*problems, "default_target.component_id must match component id")
	}
	if strings.TrimSpace(manifest.DefaultTarget.Surface) == "" {
		*problems = append(*problems, "default_target.surface is required")
	}
}

func validateModuleActions(manifest model.ComponentManifest, problems *[]string) {
	seen := make(map[string]struct{}, len(manifest.Actions))
	for _, action := range manifest.Actions {
		actionID := strings.TrimSpace(action.ActionID)
		if actionID == "" {
			*problems = append(*problems, "action.action_id is required")
		} else {
			if !isStableToken(actionID) {
				*problems = append(*problems, "action.action_id contains invalid value "+actionID)
			}
			if _, ok := seen[actionID]; ok {
				*problems = append(*problems, "actions contains duplicate action_id "+actionID)
			}
			seen[actionID] = struct{}{}
		}
		if strings.TrimSpace(action.Label) == "" {
			*problems = append(*problems, "action.label is required")
		}
		if strings.TrimSpace(action.Kind) == "" {
			*problems = append(*problems, "action.kind is required")
		} else if !isStableToken(action.Kind) {
			*problems = append(*problems, "action.kind contains invalid value "+action.Kind)
		}
		if action.OperationName != "" && !isStableToken(action.OperationName) {
			*problems = append(*problems, "action.operation_name contains invalid value "+action.OperationName)
		}
		if action.Target != nil {
			if strings.TrimSpace(action.Target.ComponentID) == "" {
				*problems = append(*problems, "action.target.component_id is required")
			} else if action.Target.ComponentID != manifest.ID {
				*problems = append(*problems, "action.target.component_id must match component id")
			}
			if strings.TrimSpace(action.Target.Surface) == "" {
				*problems = append(*problems, "action.target.surface is required")
			}
		}
	}
}

func isStableToken(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return value != ""
}

func ValidateComponentStatuses(statuses []model.ComponentStatus) error {
	var problems []string
	for _, status := range statuses {
		if !status.ManifestValid {
			if status.ValidationError != "" {
				problems = append(problems, status.ValidationError)
			} else {
				problems = append(problems, fmt.Sprintf("component manifest %s is invalid", status.ManifestPath))
			}
			continue
		}
		if !status.ShellContractCompatible {
			problems = append(problems, status.ValidationError)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("component registry validation failed: %s", strings.Join(problems, " | "))
	}
	return nil
}

func docsPresent(repoRoot string, docs []string) bool {
	if len(docs) == 0 {
		return false
	}
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			return false
		}
		docPath := doc
		if !filepath.IsAbs(docPath) {
			docPath = filepath.Join(repoRoot, docPath)
		}
		if _, err := os.Stat(docPath); err != nil {
			return false
		}
	}
	return true
}

func ApplyRuntimeDiagnostics(statuses []model.ComponentStatus, diagnostics map[string]model.ComponentRuntimeDiagnostics) []model.ComponentStatus {
	for index := range statuses {
		status := &statuses[index]
		if IsArchivedComponent(status.Manifest) {
			status.RuntimeEnabled = false
			status.RuntimeStatus = model.RuntimeStatusArchived
			continue
		}
		diag, ok := diagnostics[status.Manifest.ID]
		if !ok {
			continue
		}
		status.RuntimeEnabled = diag.Enabled
		if strings.TrimSpace(diag.Status) != "" {
			status.RuntimeStatus = diag.Status
		}
		status.LastError = diag.LastError
		status.WorkerPID = diag.WorkerPID
		status.LastHeartbeatAt = diag.LastHeartbeatAt
		status.RestartCount = diag.RestartCount
		status.InflightOperations = diag.InflightOperations
		status.LastStartAt = diag.LastStartAt
		status.LastExitCode = diag.LastExitCode
		status.LastExitReason = diag.LastExitReason
		status.RuntimeDetails = diag.RuntimeDetails
	}
	return statuses
}

func validateShellManifest(manifestPath string, manifest model.ShellManifest) error {
	if strings.TrimSpace(manifest.ID) == "" {
		return fmt.Errorf("shell manifest %s is missing id", manifestPath)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("shell manifest %s is missing name", manifestPath)
	}
	if strings.TrimSpace(manifest.ContractVersion) == "" {
		return fmt.Errorf("shell manifest %s is missing contract_version", manifestPath)
	}
	if strings.TrimSpace(manifest.ReleaseLine) == "" {
		return fmt.Errorf("shell manifest %s is missing release_line", manifestPath)
	}
	if strings.TrimSpace(manifest.ReleaseChannel) == "" {
		return fmt.Errorf("shell manifest %s is missing release_channel", manifestPath)
	}
	return nil
}
