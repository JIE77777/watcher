package codexbridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveExecutablePrefersVSCodeBundledCodexForDotCodexSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	older := filepath.Join(home, ".vscode-server", "extensions", "openai.chatgpt-26.100.0-linux-x64", "bin", "linux-x86_64")
	newer := filepath.Join(home, ".vscode-server", "extensions", "openai.chatgpt-26.417.40842-linux-x64", "bin", "linux-x86_64")
	for _, dir := range []string{older, newer} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	olderExe := filepath.Join(older, "codex")
	newerExe := filepath.Join(newer, "codex")
	writeResolveExecutable(t, olderExe)
	writeResolveExecutable(t, newerExe)
	if err := os.Chtimes(olderExe, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatalf("chtime older: %v", err)
	}
	if err := os.Chtimes(newerExe, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
		t.Fatalf("chtime newer: %v", err)
	}

	got := resolveExecutable("codex", filepath.Join(home, ".codex", "sessions"))
	if got != newerExe {
		t.Fatalf("expected bundled executable %q, got %q", newerExe, got)
	}
}

func TestResolveExecutableKeepsExplicitNonDefaultExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	explicit := filepath.Join(home, "bin", "custom-codex")
	if err := os.MkdirAll(filepath.Dir(explicit), 0o755); err != nil {
		t.Fatalf("mkdir explicit dir: %v", err)
	}
	writeResolveExecutable(t, explicit)

	got := resolveExecutable(explicit, filepath.Join(home, ".codex", "sessions"))
	if got != explicit {
		t.Fatalf("expected explicit executable %q, got %q", explicit, got)
	}
}

func TestSessionsRootToHome(t *testing.T) {
	root := filepath.Join("/tmp", "watcher", ".codex", "sessions")
	if got := sessionsRootToHome(root); got != filepath.Join("/tmp", "watcher", ".codex") {
		t.Fatalf("unexpected codex home %q", got)
	}
}

func writeResolveExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
