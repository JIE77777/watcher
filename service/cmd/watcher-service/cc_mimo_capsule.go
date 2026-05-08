package main

import (
	"strings"
	"unicode/utf8"

	"watcher/internal/model"
)

func ccMimoCapsule(shell model.ShellStatus, components []model.ComponentStatus, diagnostics []model.ShellDiagnosticEvent) map[string]any {
	componentSummaries := make([]map[string]any, 0, len(components))
	for _, status := range components {
		componentSummaries = append(componentSummaries, map[string]any{
			"id":                  status.Manifest.ID,
			"name":                firstNonBlank(status.Manifest.Name, status.Manifest.ID),
			"stage":               status.Manifest.Stage,
			"class":               status.Manifest.ComponentClass,
			"runtime_shape":       status.Manifest.RuntimeShape,
			"runtime_status":      status.RuntimeStatus,
			"enabled":             status.Enabled,
			"worker_pid":          status.WorkerPID,
			"inflight_operations": status.InflightOperations,
			"last_error":          ccMimoShortText(status.LastError, 220),
			"resources":           status.Manifest.Resources,
			"operations":          status.Manifest.Operations,
			"streams":             status.Manifest.Streams,
		})
	}

	diagnosticLimit := 4
	if len(diagnostics) < diagnosticLimit {
		diagnosticLimit = len(diagnostics)
	}
	diagnosticSummaries := make([]map[string]any, 0, diagnosticLimit)
	for _, item := range diagnostics[:diagnosticLimit] {
		diagnosticSummaries = append(diagnosticSummaries, map[string]any{
			"component_id": item.ComponentID,
			"kind":         item.Kind,
			"severity":     item.Severity,
			"message":      ccMimoShortText(item.Message, 260),
			"occurred_at":  item.OccurredAt,
		})
	}

	return map[string]any{
		"observed_at": model.NowString(),
		"shell": map[string]any{
			"id":               shell.Manifest.ID,
			"version":          shell.Version,
			"contract_version": shell.Manifest.ContractVersion,
			"service_status":   shell.ServiceStatus,
			"relay_status":     shell.RelayStatus,
			"event_bus_status": shell.EventBusStatus,
			"component_counts": shell.ComponentStats,
			"last_error":       ccMimoShortText(shell.LastError, 220),
		},
		"components":  componentSummaries,
		"diagnostics": diagnosticSummaries,
		"diagnostic_policy": map[string]any{
			"diagnostics_are_history":            true,
			"prefer_current_runtime":             true,
			"manual_restart_is_operator_action":  true,
			"avoid_overstating_recovered_events": true,
		},
	}
}

func ccMimoShortText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	n := 0
	for i := range value {
		if n >= limit {
			return value[:i] + "..."
		}
		n++
	}
	return value
}
