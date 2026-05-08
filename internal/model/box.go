package model

import "fmt"

const (
	WatcherTaskChangeAppeared    = "appeared"
	WatcherTaskChangeDisappeared = "disappeared"
	WatcherTaskChangeChanged     = "changed"
)

type WatcherTaskEvent struct {
	EventID      string   `json:"event_id"`
	TaskID       string   `json:"task_id"`
	ToolID       string   `json:"tool_id"`
	TaskName     string   `json:"task_name"`
	ResourceID   string   `json:"resource_id"`
	ItemKey      string   `json:"item_key"`
	ThreadKey    string   `json:"thread_key,omitempty"`
	SnapshotID   string   `json:"snapshot_id"`
	ItemTitle    string   `json:"item_title"`
	Summary      string   `json:"summary"`
	Body         string   `json:"body"`
	Severity     string   `json:"severity"`
	Labels       []string `json:"labels,omitempty"`
	ChangeType   string   `json:"change_type"`
	OccurredAt   string   `json:"occurred_at"`
	ExternalURL  string   `json:"external_url,omitempty"`
	ShouldDeliver bool    `json:"-"` // transient: controls outbox enqueue, not persisted
}

func WatcherTaskResourceID(taskID, itemKey, threadKey string) string {
	if threadKey != "" {
		return threadKey
	}
	if taskID != "" && itemKey != "" {
		return taskID + ":" + itemKey
	}
	return taskID
}

func (e WatcherTaskEvent) EnvelopeKind() string {
	switch e.ChangeType {
	case WatcherTaskChangeAppeared:
		return "item.appeared"
	case WatcherTaskChangeDisappeared:
		return "item.disappeared"
	default:
		return "item.changed"
	}
}

func (e WatcherTaskEvent) DisplayTitle() string {
	switch {
	case e.TaskName != "" && e.ItemTitle != "":
		return fmt.Sprintf("%s: %s", e.TaskName, e.ItemTitle)
	case e.ItemTitle != "":
		return e.ItemTitle
	default:
		return e.TaskName
	}
}
