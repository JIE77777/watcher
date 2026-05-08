package rules

import (
	"encoding/json"
	"strings"
	"testing"

	"watcher/internal/model"
)

func TestRankingOnlyChangeIgnored(t *testing.T) {
	task := model.WatchTask{
		ID:   "task_1",
		Name: "Example",
		Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}

	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey:   "FFT",
			ThreadKey: "thread_fft",
			Title:     "FFT",
			Data: map[string]any{
				"ranking": 3,
				"score":   9.8,
			},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey:   "FFT",
			ThreadKey: "thread_fft",
			Title:     "FFT",
			Data: map[string]any{
				"ranking": 2,
				"score":   9.8,
			},
		}},
	}

	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("GenerateEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events, got %d", len(events))
	}
}

func TestScoreChangeCreatesEvent(t *testing.T) {
	task := model.WatchTask{
		ID:   "task_1",
		Name: "Example",
		Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}

	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey:   "FFT",
			ThreadKey: "thread_fft",
			Title:     "FFT",
			Data: map[string]any{
				"ranking": 3,
				"score":   9.8,
			},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey:   "FFT",
			ThreadKey: "thread_fft",
			Title:     "FFT",
			Data: map[string]any{
				"ranking": 2,
				"score":   10.1,
			},
		}},
	}

	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("GenerateEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].Summary == "" || events[0].Body == "" {
		t.Fatalf("event should have summary and body: %+v", events[0])
	}
	if events[0].EnvelopeKind() != "item.changed" {
		t.Fatalf("kind = %q, want item.changed", events[0].EnvelopeKind())
	}
	if events[0].ResourceID != "thread_fft" {
		t.Fatalf("resource_id = %q, want thread_fft", events[0].ResourceID)
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

func TestAnalysisAppendedToBody(t *testing.T) {
	task := model.WatchTask{
		ID:   "task_1",
		Name: "Example",
		Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}

	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey:   "FFT",
			ThreadKey: "thread_fft",
			Title:     "FFT",
			Data:      map[string]any{"ranking": 3, "score": 9.8},
		}},
	}

	gapPrev := 5.3
	scoreDelta := 0.3
	score := 10.1
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey:   "FFT",
			ThreadKey: "thread_fft",
			Title:     "FFT",
			Identity:  map[string]any{"topic": "FFT"},
			Data:      map[string]any{"ranking": 2, "score": 10.1},
		}},
		RawMeta: map[string]any{
			"analysis": map[string]any{
				"topics": map[string]any{
					"FFT": map[string]any{
						"total_teams": 52,
						"focus_team": map[string]any{
							"rank": 2, "score": &score, "subs": 10,
							"rank_delta": 1, "score_delta": 0.3,
							"gap_to_prev": &gapPrev, "gap_to_next": nil,
						},
						"score_drops": []any{},
						"surges": []any{
							map[string]any{"team": "TeamZ", "old_rank": 20, "new_rank": 12, "rank_delta": 8},
						},
						"top_movers": []any{
							map[string]any{"team": "TeamA", "rank": 1, "score_delta": 2.1, "rank_delta": 0},
						},
					},
				},
			},
		},
	}

	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("GenerateEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}

	body := events[0].Body
	if !strings.Contains(body, "── FFT ──") {
		t.Fatalf("body should contain analysis header, got:\n%s", body)
	}
	if !strings.Contains(body, "52 队") {
		t.Fatalf("body should contain total teams, got:\n%s", body)
	}
	if !strings.Contains(body, "前10") {
		t.Fatalf("body should contain top 10 movers, got:\n%s", body)
	}
	_ = scoreDelta
}

func TestFocusTeamImmediateDelivery(t *testing.T) {
	task := model.WatchTask{
		ID: "task_1", Name: "Example", Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}
	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "FFT", ThreadKey: "thread_fft", Title: "FFT",
			NotifyMode: "immediate",
			Data:       map[string]any{"ranking": 3, "score": 9.8},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "FFT", ThreadKey: "thread_fft", Title: "FFT",
			NotifyMode: "immediate",
			Data:       map[string]any{"ranking": 2, "score": 10.1},
		}},
	}
	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].ShouldDeliver {
		t.Fatalf("focus team event should have ShouldDeliver=true")
	}
}

func TestLeaderboardNoDelivery(t *testing.T) {
	task := model.WatchTask{
		ID: "task_1", Name: "Example", Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}
	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "leaderboard:FFT:rival1", ThreadKey: "t_rival1", Title: "FFT / rival1",
			NotifyMode: "summary",
			Identity:   map[string]any{"scope": "leaderboard_team", "team_name": "rival1", "topic": "FFT"},
			Data:       map[string]any{"ranking": 15, "score": 100.0},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "leaderboard:FFT:rival1", ThreadKey: "t_rival1", Title: "FFT / rival1",
			NotifyMode: "summary",
			Identity:   map[string]any{"scope": "leaderboard_team", "team_name": "rival1", "topic": "FFT"},
			Data:       map[string]any{"ranking": 14, "score": 105.0},
		}},
	}
	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// individual leaderboard event should exist but NOT be delivered
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ShouldDeliver {
		t.Fatalf("leaderboard event should have ShouldDeliver=false, got true")
	}
}

func TestSilentLeaderboardDoesNotCreateSummary(t *testing.T) {
	task := model.WatchTask{
		ID: "task_1", Name: "Example", Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}
	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "leaderboard:FFT:rival1", ThreadKey: "t_rival1", Title: "FFT / rival1",
			Identity: map[string]any{"scope": "leaderboard_team", "team_name": "rival1", "topic": "FFT"},
			Data:     map[string]any{"ranking": 10, "score": 200.0},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "leaderboard:FFT:rival1", ThreadKey: "t_rival1", Title: "FFT / rival1",
			Identity: map[string]any{"scope": "leaderboard_team", "team_name": "rival1", "topic": "FFT"},
			Data:     map[string]any{"ranking": 8, "score": 250.0},
		}},
	}
	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected only individual silent event, got %d", len(events))
	}
	if events[0].ShouldDeliver {
		t.Fatalf("silent leaderboard event should not be delivered")
	}
	if events[0].ItemKey == "leaderboard_summary" {
		t.Fatalf("silent leaderboard should not create summary event")
	}
}

func TestLeaderboardSummaryOnTop10Change(t *testing.T) {
	task := model.WatchTask{
		ID: "task_1", Name: "Example", Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}
	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "leaderboard:FFT:rival1", ThreadKey: "t_rival1", Title: "FFT / rival1",
			NotifyMode: "summary",
			Identity:   map[string]any{"scope": "leaderboard_team", "team_name": "rival1", "topic": "FFT"},
			Data:       map[string]any{"ranking": 10, "score": 200.0},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "leaderboard:FFT:rival1", ThreadKey: "t_rival1", Title: "FFT / rival1",
			NotifyMode: "summary",
			Identity:   map[string]any{"scope": "leaderboard_team", "team_name": "rival1", "topic": "FFT"},
			Data:       map[string]any{"ranking": 8, "score": 250.0},
		}},
	}
	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// should have 2 events: individual leaderboard change + summary
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	summary := events[1]
	if !summary.ShouldDeliver {
		t.Fatalf("summary event should have ShouldDeliver=true")
	}
	if summary.ItemKey != "leaderboard_summary" {
		t.Fatalf("summary item_key = %q, want leaderboard_summary", summary.ItemKey)
	}
	if !strings.Contains(summary.Body, "rival1") {
		t.Fatalf("summary body should mention rival1, got:\n%s", summary.Body)
	}
}

func TestNoAnalysisWhenMissing(t *testing.T) {
	task := model.WatchTask{
		ID: "task_1", Name: "Example", Tool: "example_tool",
		Settings: mustJSON(t, model.TaskSettings{
			RuleOptions: model.RuleOptions{IgnoreFields: []string{"ranking"}},
		}),
	}
	prev := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "FFT", ThreadKey: "thread_fft", Title: "FFT",
			Data: map[string]any{"ranking": 3, "score": 9.8},
		}},
	}
	curr := &model.SourceSnapshot{
		TaskID: "task_1",
		Items: []model.SnapshotItem{{
			ItemKey: "FFT", ThreadKey: "thread_fft", Title: "FFT",
			Data: map[string]any{"ranking": 2, "score": 10.1},
		}},
	}
	events, err := GenerateEvents(task, prev, "snap_old", curr, "snap_new")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	body := events[0].Body
	if strings.Contains(body, "全局") {
		t.Fatalf("body should NOT contain analysis when no raw_meta, got:\n%s", body)
	}
}
