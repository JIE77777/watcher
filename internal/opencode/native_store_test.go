package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListNativeHistoryMessagesTextOnly(t *testing.T) {
	tempDir := t.TempDir()
	nativeDB := filepath.Join(tempDir, "opencode.db")
	db, err := sql.Open("sqlite3", nativeDB)
	if err != nil {
		t.Fatalf("open native db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE message (
			id text PRIMARY KEY,
			session_id text NOT NULL,
			time_created integer NOT NULL,
			time_updated integer NOT NULL,
			data text NOT NULL
		);
		CREATE TABLE part (
			id text PRIMARY KEY,
			message_id text NOT NULL,
			session_id text NOT NULL,
			time_created integer NOT NULL,
			time_updated integer NOT NULL,
			data text NOT NULL
		);
	`); err != nil {
		t.Fatalf("create native history schema: %v", err)
	}
	nowMS := time.Now().UnixMilli()
	if _, err := db.Exec(`
		INSERT INTO message (id, session_id, time_created, time_updated, data)
		VALUES
			('msg_user', 'ses_history_1', ?, ?, ?),
			('msg_assistant', 'ses_history_1', ?, ?, ?);
	`,
		nowMS, nowMS, fmt.Sprintf(`{"role":"user","time":{"created":%d},"model":{"providerID":"test-provider","modelID":"test-model"}}`, nowMS),
		nowMS+1, nowMS+2, fmt.Sprintf(`{"role":"assistant","time":{"created":%d,"completed":%d},"tokens":{"total":12,"input":5,"output":7},"providerID":"test-provider","modelID":"test-model"}`, nowMS+1, nowMS+2),
	); err != nil {
		t.Fatalf("insert native messages: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		VALUES
			('prt_user_text', 'msg_user', 'ses_history_1', ?, ?, '{"type":"text","text":"手机能看到历史吗"}'),
			('prt_reasoning', 'msg_assistant', 'ses_history_1', ?, ?, '{"type":"reasoning","text":"hidden reasoning"}'),
			('prt_assistant_text', 'msg_assistant', 'ses_history_1', ?, ?, '{"type":"text","text":"可以，只展示可读回复。"}'),
			('prt_step_finish', 'msg_assistant', 'ses_history_1', ?, ?, '{"type":"step-finish"}')`,
		nowMS, nowMS,
		nowMS+1, nowMS+1,
		nowMS+2, nowMS+2,
		nowMS+3, nowMS+3,
	); err != nil {
		t.Fatalf("insert native parts: %v", err)
	}

	messages, err := ListNativeHistoryMessages(context.Background(), nativeDB, "ses_history_1", 20)
	if err != nil {
		t.Fatalf("ListNativeHistoryMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Text != "手机能看到历史吗" {
		t.Fatalf("user message = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Text != "可以，只展示可读回复。" {
		t.Fatalf("assistant message = %+v", messages[1])
	}
	if strings.Contains(messages[1].Text, "hidden reasoning") {
		t.Fatalf("reasoning leaked: %+v", messages[1])
	}
	if messages[1].HiddenPartCount != 2 {
		t.Fatalf("hidden part count = %d, want 2", messages[1].HiddenPartCount)
	}
	if total := anyInt64(messages[1].Tokens["total"]); total != 12 {
		t.Fatalf("tokens total = %d, want 12", total)
	}
}

func TestNativeSessionBusyInfoReturnsLatestUnfinishedAssistant(t *testing.T) {
	tempDir := t.TempDir()
	nativeDB := filepath.Join(tempDir, "opencode.db")
	db, err := sql.Open("sqlite3", nativeDB)
	if err != nil {
		t.Fatalf("open native db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE message (
			id text PRIMARY KEY,
			session_id text NOT NULL,
			time_created integer NOT NULL,
			time_updated integer NOT NULL,
			data text NOT NULL
		);
	`); err != nil {
		t.Fatalf("create message schema: %v", err)
	}
	nowMS := time.Now().UnixMilli()
	oldMS := time.Now().Add(-3 * time.Hour).UnixMilli()
	if _, err := db.Exec(`
		INSERT INTO message (id, session_id, time_created, time_updated, data)
		VALUES
			('msg_done', 'ses_busy_1', ?, ?, '{"role":"assistant","time":{"completed":1}}'),
			('msg_old', 'ses_busy_1', ?, ?, '{"role":"assistant","time":{"created":1}}'),
			('msg_busy', 'ses_busy_1', ?, ?, '{"role":"assistant","parentID":"msg_user","time":{"created":1}}')`,
		nowMS-30, nowMS-20,
		oldMS, oldMS,
		nowMS-10, nowMS,
	); err != nil {
		t.Fatalf("insert messages: %v", err)
	}

	info, busy := NativeSessionBusyInfo(context.Background(), nativeDB, "ses_busy_1")
	if !busy {
		t.Fatal("NativeSessionBusyInfo busy = false, want true")
	}
	if info.MessageID != "msg_busy" || info.ParentMessageID != "msg_user" || info.TimeUpdatedMS != nowMS {
		t.Fatalf("busy info = %+v", info)
	}
}
