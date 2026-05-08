package main

import (
	"net/http"
	"strings"

	"watcher/internal/model"
)

func (a *App) handleModulesV2(w http.ResponseWriter, _ *http.Request) {
	shell, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"shell_contract": shell.Manifest.ContractVersion,
		"modules":        buildModuleDescriptors(componentStatuses),
	})
}

func (a *App) handleModuleV2(w http.ResponseWriter, r *http.Request) {
	componentID := strings.TrimSpace(r.PathValue("componentID"))
	if componentID == "" {
		http.Error(w, "module id is required", http.StatusBadRequest)
		return
	}
	_, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status, ok := findComponentStatus(componentStatuses, componentID)
	if !ok {
		http.Error(w, "module not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"module": moduleDescriptorFromStatus(status),
	})
}

func buildModuleDescriptors(statuses []model.ComponentStatus) []model.ModuleDescriptor {
	modules := make([]model.ModuleDescriptor, 0, len(statuses))
	for _, status := range statuses {
		modules = append(modules, moduleDescriptorFromStatus(status))
	}
	return modules
}

func moduleDescriptorFromStatus(status model.ComponentStatus) model.ModuleDescriptor {
	manifest := status.Manifest
	surfaces := normalizedModuleSurfaces(manifest)
	return model.ModuleDescriptor{
		ComponentID:   manifest.ID,
		Name:          manifest.Name,
		Version:       manifest.Version,
		Stage:         manifest.Stage,
		Status:        firstNonBlank(status.RuntimeStatus, model.RuntimeStatusInvalid),
		RuntimeShape:  manifest.RuntimeShape,
		ManifestValid: status.ManifestValid,
		Capabilities:  nonNilStrings(manifest.Capabilities),
		Surfaces:      nonNilModuleSurfaces(surfaces),
		DefaultTarget: normalizedModuleDefaultTarget(manifest, surfaces),
		Actions:       nonNilModuleActions(manifest.Actions),
		Streams:       nonNilStrings(manifest.Streams),
		Resources:     nonNilStrings(manifest.Resources),
		Operations:    nonNilStrings(manifest.Operations),
	}
}

func normalizedModuleSurfaces(manifest model.ComponentManifest) []model.ModuleSurface {
	if len(manifest.Surfaces) > 0 {
		surfaces := make([]model.ModuleSurface, 0, len(manifest.Surfaces))
		for _, surface := range manifest.Surfaces {
			surface.ID = strings.TrimSpace(surface.ID)
			if surface.Target.ComponentID == "" {
				surface.Target.ComponentID = manifest.ID
			}
			if surface.Target.Surface == "" {
				surface.Target.Surface = surface.ID
			}
			surfaces = append(surfaces, surface)
		}
		return surfaces
	}

	surfaces := make([]model.ModuleSurface, 0, len(manifest.AndroidSurfaces))
	for index, surfaceID := range manifest.AndroidSurfaces {
		surfaceID = strings.TrimSpace(surfaceID)
		if surfaceID == "" {
			continue
		}
		surfaces = append(surfaces, model.ModuleSurface{
			ID:      surfaceID,
			Title:   surfaceTitle(surfaceID),
			Kind:    "legacy_android",
			Primary: index == 0,
			Target: model.ShellTarget{
				ComponentID: manifest.ID,
				Surface:     surfaceID,
			},
		})
	}
	return surfaces
}

func normalizedModuleDefaultTarget(manifest model.ComponentManifest, surfaces []model.ModuleSurface) model.ShellTarget {
	if manifest.DefaultTarget != nil {
		target := *manifest.DefaultTarget
		if target.ComponentID == "" {
			target.ComponentID = manifest.ID
		}
		return target
	}
	for _, surface := range surfaces {
		if surface.Primary {
			return surface.Target
		}
	}
	if len(surfaces) > 0 {
		return surfaces[0].Target
	}
	return model.ShellTarget{ComponentID: manifest.ID, Surface: "home"}
}

func surfaceTitle(surfaceID string) string {
	value := strings.ReplaceAll(surfaceID, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilModuleSurfaces(values []model.ModuleSurface) []model.ModuleSurface {
	if values == nil {
		return []model.ModuleSurface{}
	}
	return values
}

func nonNilModuleActions(values []model.ModuleAction) []model.ModuleAction {
	if values == nil {
		return []model.ModuleAction{}
	}
	return values
}
