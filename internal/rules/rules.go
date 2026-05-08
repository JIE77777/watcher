package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"watcher/internal/model"
)

func GenerateEvents(task model.WatchTask, previous *model.SourceSnapshot, previousSnapshotID string, current *model.SourceSnapshot, currentSnapshotID string) ([]model.WatcherTaskEvent, error) {
	settings, err := model.ParseTaskSettings(task.Settings)
	if err != nil {
		return nil, fmt.Errorf("parse task settings: %w", err)
	}
	opts := settings.RuleOptions.WithDefaults()

	oldByKey := indexItems(previous)
	newByKey := indexItems(current)

	keys := make([]string, 0, len(oldByKey)+len(newByKey))
	keySet := make(map[string]struct{})
	for key := range oldByKey {
		keySet[key] = struct{}{}
	}
	for key := range newByKey {
		keySet[key] = struct{}{}
	}
	for key := range keySet {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var events []model.WatcherTaskEvent
	var leaderboardChanges []leaderboardChangeRecord

	for _, key := range keys {
		oldItem := oldByKey[key]
		newItem := newByKey[key]

		notifyMode := itemNotifyMode(oldItem, newItem)
		isImmediate := notifyMode == "immediate"

		switch {
		case oldItem == nil && newItem != nil:
			if previous == nil && !opts.NotifyOnInitial {
				continue
			}
			if opts.NotifyOnAppear != nil && !*opts.NotifyOnAppear {
				continue
			}
			body := fmt.Sprintf("%s 新出现。\n当前数据：\n%s", newItem.Title, formatDataLines(newItem.Data))
			if analysis := formatAnalysisSummary(current, newItem); analysis != "" {
				body += "\n\n" + analysis
			}
			ev := newEvent(task, currentSnapshotID, current, newItem, opts.Severity, model.WatcherTaskChangeAppeared, "新出现", body)
			ev.ShouldDeliver = isImmediate
			events = append(events, ev)

		case oldItem != nil && newItem == nil:
			if opts.NotifyOnDisappear != nil && !*opts.NotifyOnDisappear {
				continue
			}
			body := fmt.Sprintf("%s 离开监控范围。\n此前数据：\n%s", oldItem.Title, formatDataLines(oldItem.Data))
			ev := newEvent(task, previousSnapshotID, current, oldItem, opts.Severity, model.WatcherTaskChangeDisappeared, "离开监控范围", body)
			ev.ShouldDeliver = isImmediate
			events = append(events, ev)

		case oldItem != nil && newItem != nil:
			changes := diffItem(*oldItem, *newItem, opts)
			if len(changes) == 0 {
				continue
			}
			body := strings.Join(changes, "\n")
			if analysis := formatAnalysisSummary(current, newItem); analysis != "" {
				body += "\n\n" + analysis
			}
			ev := newEvent(task, currentSnapshotID, current, newItem, opts.Severity, model.WatcherTaskChangeChanged, changes[0], body)
			ev.ShouldDeliver = isImmediate
			events = append(events, ev)

			// collect leaderboard changes for summary
			if notifyMode == "summary" && newItem.Identity != nil {
				if scope, _ := newItem.Identity["scope"].(string); scope == "leaderboard_team" {
					leaderboardChanges = append(leaderboardChanges, leaderboardChangeRecord{
						item:    newItem,
						changes: changes,
					})
				}
			}
		}
	}

	// generate summary event for significant leaderboard changes
	if len(leaderboardChanges) > 0 && current != nil {
		if summaryEv, ok := buildLeaderboardSummaryEvent(task, currentSnapshotID, current, leaderboardChanges); ok {
			events = append(events, summaryEv)
		}
	}

	return events, nil
}

type leaderboardChangeRecord struct {
	item    *model.SnapshotItem
	changes []string
}

func itemNotifyMode(oldItem, newItem *model.SnapshotItem) string {
	if newItem != nil && newItem.NotifyMode != "" {
		return newItem.NotifyMode
	}
	if oldItem != nil {
		return oldItem.NotifyMode
	}
	return ""
}

func buildLeaderboardSummaryEvent(
	task model.WatchTask,
	snapshotID string,
	snapshot *model.SourceSnapshot,
	changes []leaderboardChangeRecord,
) (model.WatcherTaskEvent, bool) {
	topicChanges := make(map[string][]leaderboardChangeRecord)
	for _, c := range changes {
		topic := extractTopic(c.item)
		if topic != "" {
			topicChanges[topic] = append(topicChanges[topic], c)
		}
	}

	var blocks []string
	significantCount := 0
	for topic, records := range topicChanges {
		var topLines []string
		for _, r := range records {
			if r.item.Identity == nil {
				continue
			}
			if scope, _ := r.item.Identity["scope"].(string); scope != "leaderboard_team" {
				continue
			}
			rank := toFloat(r.item.Data["ranking"])
			if !(rank > 0 && rank <= 10) {
				continue
			}
			teamName, _ := r.item.Identity["team_name"].(string)
			topLines = append(topLines, formatLeaderboardSummaryLine(int(rank), teamName, r.item.Data, r.changes))
		}
		if len(topLines) > 0 {
			sort.Strings(topLines)
			blocks = append(blocks, fmt.Sprintf("── %s 前10变动 ──", topic))
			blocks = append(blocks, topLines...)
			significantCount++
		}
	}

	if significantCount == 0 {
		return model.WatcherTaskEvent{}, false
	}

	body := strings.Join(blocks, "\n")
	occurredAt := model.NowString()
	if snapshot.FetchedAt != "" {
		occurredAt = snapshot.FetchedAt
	}

	ev := model.WatcherTaskEvent{
		EventID:       eventID(task.ID, "leaderboard_summary", occurredAt, body),
		TaskID:        task.ID,
		ToolID:        task.Tool,
		TaskName:      task.Name,
		ResourceID:    task.ID + ":leaderboard_summary",
		ItemKey:       "leaderboard_summary",
		SnapshotID:    snapshotID,
		ItemTitle:     "排行榜前10变动",
		Summary:       fmt.Sprintf("前10榜单变动：%d 个赛道", significantCount),
		Body:          body,
		Severity:      model.DefaultSeverity,
		Labels:        model.UniqueStrings(task.Labels, []string{task.Tool, "leaderboard_summary"}),
		ChangeType:    model.WatcherTaskChangeChanged,
		OccurredAt:    occurredAt,
		ShouldDeliver: true,
	}
	return ev, true
}

type analysisData struct {
	Topics map[string]topicAnalysis `json:"topics"`
}

func formatLeaderboardSummaryLine(rank int, teamName string, data map[string]any, changes []string) string {
	parts := []string{fmt.Sprintf("  #%02d %s", rank, teamName)}
	if score, ok := data["takeTime"]; ok {
		parts = append(parts, "耗时 "+formatMetric("takeTime", score))
	}
	if subs, ok := data["commitTimes"]; ok {
		parts = append(parts, "提交 "+formatMetric("commitTimes", subs))
	}
	if last, ok := data["lastCommit"]; ok {
		parts = append(parts, "最后 "+formatMetric("lastCommit", last))
	}
	if len(changes) > 0 {
		parts = append(parts, "变化 "+strings.Join(changes, "；"))
	}
	return strings.Join(parts, " | ")
}

func indexItems(snapshot *model.SourceSnapshot) map[string]*model.SnapshotItem {
	out := make(map[string]*model.SnapshotItem)
	if snapshot == nil {
		return out
	}
	for idx := range snapshot.Items {
		item := snapshot.Items[idx]
		out[item.ItemKey] = &item
	}
	return out
}

var fieldLabels = map[string]string{
	"总性能":         "得分",
	"总耗时":         "耗时",
	"提交次数":        "提交",
	"最后提交时间":      "最后提交",
	"ranking":     "排名",
	"takeTime":    "耗时",
	"commitTimes": "提交",
	"lastCommit":  "最后提交",
	"validScore":  "有效",
}

func labelField(key string) string {
	if l, ok := fieldLabels[key]; ok {
		return l
	}
	return key
}

func formatMetric(key string, v any) string {
	if v == nil {
		return "-"
	}
	switch n := v.(type) {
	case float64:
		if key == "ranking" || key == "commitTimes" {
			return fmt.Sprintf("%.0f", n)
		}
		if key == "takeTime" {
			return fmt.Sprintf("%s us", formatMetric("", n))
		}
		abs := n
		if abs < 0 {
			abs = -abs
		}
		if abs >= 10000 || (abs > 0 && abs < 1) {
			return fmt.Sprintf("%.4g", n)
		}
		if abs == float64(int64(abs)) {
			return fmt.Sprintf("%.0f", n)
		}
		return fmt.Sprintf("%.1f", n)
	case json.Number:
		f, _ := n.Float64()
		return formatMetric(key, f)
	case string:
		if n == "" {
			return "-"
		}
		return n
	case bool:
		if n {
			return "是"
		}
		return "否"
	default:
		return valueString(v)
	}
}

func diffItem(oldItem, newItem model.SnapshotItem, opts model.RuleOptions) []string {
	ignored := make(map[string]struct{}, len(opts.IgnoreFields))
	for _, field := range opts.IgnoreFields {
		ignored[field] = struct{}{}
	}

	var changes []string
	if oldItem.ExternalURL != newItem.ExternalURL {
		changes = append(changes, "external_url changed")
	}

	keys := make([]string, 0, len(oldItem.Data)+len(newItem.Data))
	keySet := make(map[string]struct{})
	for key := range oldItem.Data {
		keySet[key] = struct{}{}
	}
	for key := range newItem.Data {
		keySet[key] = struct{}{}
	}
	for key := range keySet {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if _, skip := ignored[key]; skip {
			continue
		}
		oldValue, oldOK := oldItem.Data[key]
		newValue, newOK := newItem.Data[key]
		if oldOK == newOK && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		oldStr := formatMetric(key, oldValue)
		newStr := formatMetric(key, newValue)
		changes = append(changes, fmt.Sprintf("%s: %s → %s", labelField(key), oldStr, newStr))
	}

	return changes
}

func newEvent(task model.WatchTask, snapshotID string, snapshot *model.SourceSnapshot, item *model.SnapshotItem, severity, changeType, summary, body string) model.WatcherTaskEvent {
	occurredAt := model.NowString()
	if snapshot != nil && snapshot.FetchedAt != "" {
		occurredAt = snapshot.FetchedAt
	}
	return model.WatcherTaskEvent{
		EventID:     eventID(task.ID, item.ThreadKey, occurredAt, body),
		TaskID:      task.ID,
		ToolID:      task.Tool,
		TaskName:    task.Name,
		ResourceID:  model.WatcherTaskResourceID(task.ID, item.ItemKey, item.ThreadKey),
		ItemKey:     item.ItemKey,
		ThreadKey:   item.ThreadKey,
		SnapshotID:  snapshotID,
		ItemTitle:   item.Title,
		Summary:     summary,
		Body:        body,
		Severity:    severity,
		Labels:      model.UniqueStrings(task.Labels, item.Labels, []string{task.Tool}),
		ChangeType:  changeType,
		OccurredAt:  occurredAt,
		ExternalURL: item.ExternalURL,
	}
}

func eventID(taskID, threadKey, occurredAt, body string) string {
	sum := sha256.Sum256([]byte(taskID + "|" + threadKey + "|" + occurredAt + "|" + body))
	return "evt_" + hex.EncodeToString(sum[:12])
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func valueString(v any) string {
	if v == nil {
		return "<nil>"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func formatDataLines(data map[string]any) string {
	if len(data) == 0 {
		return "  (empty)"
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("  - %s: %s", labelField(key), formatMetric(key, data[key])))
	}
	return strings.Join(lines, "\n")
}

func extractTopic(item *model.SnapshotItem) string {
	if item.Identity != nil {
		if t, ok := item.Identity["topic"].(string); ok && t != "" {
			return t
		}
	}
	for _, label := range item.Labels {
		if label == "focus_team" || label == "leaderboard_team" || label == "rival" || label == "kunpeng" || label == "leaderboard" {
			continue
		}
		return label
	}
	return ""
}

func formatAnalysisSummary(snapshot *model.SourceSnapshot, item *model.SnapshotItem) string {
	if snapshot == nil || snapshot.RawMeta == nil {
		return ""
	}
	analysisRaw, ok := snapshot.RawMeta["analysis"]
	if !ok {
		return ""
	}
	analysisJSON, err := json.Marshal(analysisRaw)
	if err != nil {
		return ""
	}
	var analysis struct {
		Topics map[string]topicAnalysis `json:"topics"`
	}
	if err := json.Unmarshal(analysisJSON, &analysis); err != nil {
		return ""
	}

	topic := extractTopic(item)
	if topic == "" {
		return ""
	}
	ta, ok := analysis.Topics[topic]
	if !ok {
		return ""
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("── %s ──", topic))

	// summary line with focus team
	totalStr := fmt.Sprintf("%d 队", ta.TotalTeams)
	if ta.FocusTeam != nil {
		ft := ta.FocusTeam
		focusName := extractFocusTeam(snapshot)
		ftLine := fmt.Sprintf("%s #%d", focusName, ft.Rank)
		if ft.Score != nil {
			ftLine += fmt.Sprintf(" (%s)", formatMetric("", *ft.Score))
		}
		if ft.RankDelta != 0 {
			arrow := "↑"
			if ft.RankDelta < 0 {
				arrow = "↓"
				ft.RankDelta = -ft.RankDelta
			}
			ftLine += fmt.Sprintf(" %s%d", arrow, ft.RankDelta)
		}
		if ft.GapToPrev != nil {
			ftLine += fmt.Sprintf(" 距上家 %s", formatMetric("", *ft.GapToPrev))
		}
		lines = append(lines, totalStr+" | "+ftLine)
	} else {
		lines = append(lines, totalStr)
	}

	// top 10 movers
	if len(ta.TopMovers) > 0 {
		parts := make([]string, 0, len(ta.TopMovers))
		for _, m := range ta.TopMovers {
			sign := "+"
			if m.ScoreDelta < 0 {
				sign = ""
			}
			parts = append(parts, fmt.Sprintf("%s #%d(%s%s)", m.Team, m.Rank, sign, formatMetric("", m.ScoreDelta)))
		}
		lines = append(lines, "前10: "+strings.Join(parts, " "))
	}

	return strings.Join(lines, "\n")
}

func extractFocusTeam(snapshot *model.SourceSnapshot) string {
	if snapshot.RawMeta == nil {
		return "?"
	}
	if tn, ok := snapshot.RawMeta["team_name"].(string); ok && tn != "" {
		return tn
	}
	return "?"
}

type topicAnalysis struct {
	TotalTeams int             `json:"total_teams"`
	FocusTeam  *focusTeamInfo  `json:"focus_team"`
	ScoreDrops []scoreDropInfo `json:"score_drops"`
	Surges     []surgeInfo     `json:"surges"`
	TopMovers  []topMoverInfo  `json:"top_movers"`
}

type focusTeamInfo struct {
	Rank       int      `json:"rank"`
	Score      *float64 `json:"score"`
	Subs       int      `json:"subs"`
	RankDelta  int      `json:"rank_delta"`
	ScoreDelta float64  `json:"score_delta"`
	GapToPrev  *float64 `json:"gap_to_prev"`
	GapToNext  *float64 `json:"gap_to_next"`
}

type scoreDropInfo struct {
	Team     string  `json:"team"`
	OldScore float64 `json:"old_score"`
	NewScore float64 `json:"new_score"`
	Delta    float64 `json:"delta"`
}

type surgeInfo struct {
	Team      string `json:"team"`
	OldRank   int    `json:"old_rank"`
	NewRank   int    `json:"new_rank"`
	RankDelta int    `json:"rank_delta"`
}

type topMoverInfo struct {
	Team       string  `json:"team"`
	Rank       int     `json:"rank"`
	ScoreDelta float64 `json:"score_delta"`
	RankDelta  int     `json:"rank_delta"`
}
