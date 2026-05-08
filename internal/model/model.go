package model

import "encoding/json"

type DeliveryType string

const (
	DeliveryDesktop   DeliveryType = "desktop"
	DeliveryRelayPush DeliveryType = "relay_push"
	DeliveryAndroid   DeliveryType = "android_device"
	DeliveryWebhook   DeliveryType = "webhook"
	DefaultSeverity                = "info"
)

type DeliveryTarget struct {
	Type     DeliveryType      `json:"type"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	DeviceID string            `json:"device_id,omitempty"`
}

type WatchTask struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Tool            string           `json:"tool"`
	Enabled         bool             `json:"enabled"`
	ScheduleSeconds int              `json:"schedule_seconds"`
	Settings        json.RawMessage  `json:"settings"`
	DeliveryTargets []DeliveryTarget `json:"delivery_targets"`
	Labels          []string         `json:"labels,omitempty"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
	LastRunAt       string           `json:"last_run_at,omitempty"`
	LastStatus      string           `json:"last_status,omitempty"`
	LastError       string           `json:"last_error,omitempty"`
}

type SnapshotItem struct {
	ItemKey     string         `json:"item_key"`
	ThreadKey   string         `json:"thread_key"`
	Title       string         `json:"title"`
	Identity    map[string]any `json:"identity,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
	ExternalURL string         `json:"external_url,omitempty"`
	Labels      []string       `json:"labels,omitempty"`
	NotifyMode  string         `json:"notify_mode,omitempty"` // "immediate" | "summary" | ""
}

type SourceSnapshot struct {
	SourceID  string         `json:"source_id"`
	TaskID    string         `json:"task_id"`
	FetchedAt string         `json:"fetched_at"`
	Version   string         `json:"version"`
	Items     []SnapshotItem `json:"items"`
	RawMeta   map[string]any `json:"raw_meta,omitempty"`
}

type RuleOptions struct {
	IgnoreFields      []string `json:"ignore_fields,omitempty"`
	NotifyOnInitial   bool     `json:"notify_on_initial,omitempty"`
	NotifyOnAppear    *bool    `json:"notify_on_appear,omitempty"`
	NotifyOnDisappear *bool    `json:"notify_on_disappear,omitempty"`
	Severity          string   `json:"severity,omitempty"`
}

func (r RuleOptions) WithDefaults() RuleOptions {
	if r.Severity == "" {
		r.Severity = DefaultSeverity
	}
	if r.NotifyOnAppear == nil {
		v := true
		r.NotifyOnAppear = &v
	}
	if r.NotifyOnDisappear == nil {
		v := true
		r.NotifyOnDisappear = &v
	}
	return r
}

type TaskSettings struct {
	ToolConfig  json.RawMessage `json:"tool_config"`
	RuleOptions RuleOptions     `json:"rule_options"`
}

func ParseTaskSettings(raw json.RawMessage) (TaskSettings, error) {
	if len(raw) == 0 {
		return TaskSettings{}, nil
	}
	var settings TaskSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return TaskSettings{}, err
	}
	return settings, nil
}

type SourceRef struct {
	TaskID     string `json:"task_id"`
	ToolID     string `json:"tool_id"`
	SnapshotID string `json:"snapshot_id"`
	ItemKey    string `json:"item_key"`
}

type DeviceRegistration struct {
	DeviceID    string `json:"device_id"`
	Platform    string `json:"platform"`
	DeviceName  string `json:"device_name,omitempty"`
	PushToken   string `json:"push_token,omitempty"`
	DeviceToken string `json:"device_token,omitempty"`
	LastCursor  int64  `json:"last_cursor"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func UniqueStrings(values ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, group := range values {
		for _, item := range group {
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}
