package codexbridge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func DefaultCodexHome() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func resolveExecutable(requested string, sessionsRoot string) string {
	requested = strings.TrimSpace(requested)
	if sessionsRoot == "" {
		sessionsRoot = DefaultSessionsRoot()
	}
	if requested == "" || isDefaultExecutableName(requested) {
		if detected := detectVSCodeExecutable(sessionsRoot); detected != "" {
			return detected
		}
		if requested == "" {
			return "codex"
		}
	}
	return requested
}

func isDefaultExecutableName(value string) bool {
	base := filepath.Base(strings.TrimSpace(value))
	return base == "codex" || base == "codex.exe"
}

func detectVSCodeExecutable(sessionsRoot string) string {
	if !isVSCodeCodexHome(sessionsRootToHome(sessionsRoot)) {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return detectVSCodeExecutableFromRoots([]string{
		filepath.Join(home, ".vscode-server", "extensions"),
		filepath.Join(home, ".vscode", "extensions"),
		filepath.Join(home, ".cursor-server", "extensions"),
		filepath.Join(home, ".cursor", "extensions"),
	})
}

func detectVSCodeExecutableFromRoots(roots []string) string {
	type candidate struct {
		path    string
		modTime int64
	}
	var candidates []candidate
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		pattern := filepath.Join(root, "openai.chatgpt-*", "bin", "*", "codex*")
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			candidates = append(candidates, candidate{
				path:    match,
				modTime: info.ModTime().UnixNano(),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].modTime == candidates[j].modTime {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime > candidates[j].modTime
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].path
}

func sessionsRootToHome(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return DefaultCodexHome()
	}
	root = filepath.Clean(root)
	if filepath.Base(root) == "sessions" {
		return filepath.Dir(root)
	}
	return root
}

func isVSCodeCodexHome(root string) bool {
	return filepath.Base(filepath.Clean(strings.TrimSpace(root))) == ".codex"
}

func shouldUseVSCodeOriginator(meta SessionMeta, sessionsRoot string) bool {
	originator := strings.ToLower(strings.TrimSpace(meta.Originator))
	if strings.Contains(originator, "vscode") {
		return true
	}
	return isVSCodeCodexHome(sessionsRootToHome(sessionsRoot))
}
