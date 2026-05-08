package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"watcher/internal/model"
)

const hostComponentID = "host"

type HostConfig struct {
	FileRoots        []HostFileRootConfig `json:"file_roots"`
	MaxDownloadBytes int64                `json:"max_download_bytes"`
	ShowHidden       bool                 `json:"show_hidden"`
}

type HostFileRootConfig struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Path     string `json:"path"`
	Download bool   `json:"download"`
}

type hostOverview struct {
	Hostname      string            `json:"hostname"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	CPU           hostCPUOverview   `json:"cpu"`
	Load          []float64         `json:"load"`
	Memory        hostMemory        `json:"memory"`
	Disks         []hostDisk        `json:"disks"`
	FileRoots     []hostFileRoot    `json:"file_roots"`
	ServerTime    string            `json:"server_time"`
	Limits        map[string]int64  `json:"limits"`
	Runtime       map[string]string `json:"runtime"`
}

type hostCPUOverview struct {
	Cores        int     `json:"cores"`
	LoadPercent  float64 `json:"load_percent"`
	LoadAverage1 float64 `json:"load_average_1"`
}

type hostMemory struct {
	TotalBytes     int64   `json:"total_bytes"`
	AvailableBytes int64   `json:"available_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

type hostDisk struct {
	RootID         string  `json:"root_id"`
	Label          string  `json:"label"`
	Path           string  `json:"path"`
	TotalBytes     int64   `json:"total_bytes"`
	AvailableBytes int64   `json:"available_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

type hostFileRoot struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Download  bool   `json:"download"`
	Source    string `json:"source"`
	Removable bool   `json:"removable"`
}

type hostFileEntry struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	ModifiedAt      string `json:"modified_at,omitempty"`
	Download        bool   `json:"download"`
	TargetRootID    string `json:"target_root_id,omitempty"`
	TargetRootLabel string `json:"target_root_label,omitempty"`
	TargetDownload  bool   `json:"target_download,omitempty"`
}

type hostFileRootCreateRequest struct {
	Label    string `json:"label"`
	Path     string `json:"path"`
	Download *bool  `json:"download"`
}

func (a *App) handleHostOverviewV2(w http.ResponseWriter, _ *http.Request) {
	overview := a.hostOverview()
	writeJSON(w, http.StatusOK, map[string]any{"overview": overview})
}

func (a *App) handleHostFilesV2(w http.ResponseWriter, r *http.Request) {
	root, absPath, relPath, err := a.resolveHostFilePath(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	roots := a.hostFileRoots()
	items := make([]hostFileEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !a.cfg.Host.ShowHidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		entryRel := filepath.ToSlash(filepath.Join(relPath, name))
		item := hostFileEntry{
			Name:       name,
			Path:       entryRel,
			Kind:       kind,
			SizeBytes:  fileSizeForKind(kind, info.Size()),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			Download:   kind == "file" && root.Download && a.fileWithinHostDownloadLimit(info.Size()),
		}
		if kind == "directory" {
			entryAbs := filepath.Clean(filepath.Join(absPath, name))
			if resolved, err := filepath.EvalSymlinks(entryAbs); err == nil {
				entryAbs = resolved
			}
			if target := hostFileRootForPath(roots, root.ID, entryAbs); target.ID != "" {
				item.TargetRootID = target.ID
				item.TargetRootLabel = target.Label
				item.TargetDownload = target.Download
			}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "directory"
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"root":    root,
		"path":    filepath.ToSlash(relPath),
		"entries": items,
	})
}

func (a *App) handleHostFileDownloadV2(w http.ResponseWriter, r *http.Request) {
	root, absPath, _, err := a.resolveHostFilePath(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !root.Download {
		http.Error(w, "download is disabled for this root", http.StatusForbidden)
		return
	}
	info, err := os.Stat(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "cannot download a directory", http.StatusBadRequest)
		return
	}
	if !a.fileWithinHostDownloadLimit(info.Size()) {
		http.Error(w, "file exceeds max_download_bytes", http.StatusRequestEntityTooLarge)
		return
	}
	file, err := os.Open(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	defer file.Close()

	filename := filepath.Base(absPath)
	if contentType := mime.TypeByExtension(filepath.Ext(filename)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeDownloadFilename(filename)+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (a *App) handleHostFileRootCreateV2(w http.ResponseWriter, r *http.Request) {
	var input hostFileRootCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	path, err := a.normalizeHostRootPath(input.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, existing := range a.hostFileRoots() {
		if existing.Path == path {
			writeJSON(w, http.StatusOK, map[string]any{
				"root":  existing,
				"roots": a.hostFileRoots(),
			})
			return
		}
	}
	if a.store == nil {
		http.Error(w, "host root store is unavailable", http.StatusServiceUnavailable)
		return
	}
	label := strings.TrimSpace(input.Label)
	if label == "" {
		label = hostDefaultLabelForPath(path)
	}
	download := true
	if input.Download != nil {
		download = *input.Download
	}
	saved, err := a.store.SaveHostFileRoot(model.HostSavedFileRoot{
		RootID:   model.NewID("hostroot"),
		Label:    label,
		Path:     path,
		Download: download,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	root := hostRootFromSaved(saved)
	writeJSON(w, http.StatusCreated, map[string]any{
		"root":  root,
		"roots": a.hostFileRoots(),
	})
}

func (a *App) handleHostFileRootDeleteV2(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.PathValue("rootID"))
	if rootID == "" {
		http.Error(w, "root id is required", http.StatusBadRequest)
		return
	}
	if a.store == nil {
		http.Error(w, "host root store is unavailable", http.StatusServiceUnavailable)
		return
	}
	deleted, err := a.store.DeleteHostFileRoot(rootID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "custom file root not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"roots":   a.hostFileRoots(),
	})
}

func (a *App) hostOverview() hostOverview {
	hostname, _ := os.Hostname()
	loads := hostLoadAverage()
	cores := runtime.NumCPU()
	loadPercent := 0.0
	if cores > 0 && len(loads) > 0 {
		loadPercent = clampPercent(loads[0] / float64(cores) * 100)
	}
	roots := a.hostFileRoots()
	disks := make([]hostDisk, 0, len(roots))
	for _, root := range roots {
		if disk, ok := hostDiskForRoot(root); ok {
			disks = append(disks, disk)
		}
	}
	return hostOverview{
		Hostname:      hostname,
		UptimeSeconds: hostUptimeSeconds(),
		CPU:           hostCPUOverview{Cores: cores, LoadPercent: loadPercent, LoadAverage1: firstFloat(loads)},
		Load:          loads,
		Memory:        hostMemoryOverview(),
		Disks:         disks,
		FileRoots:     roots,
		ServerTime:    model.NowString(),
		Limits:        map[string]int64{"max_download_bytes": a.hostMaxDownloadBytes()},
		Runtime: map[string]string{
			"goos":   runtime.GOOS,
			"goarch": runtime.GOARCH,
		},
	}
}

func (a *App) hostFileRoots() []hostFileRoot {
	roots := make([]hostFileRoot, 0, len(a.cfg.Host.FileRoots))
	seen := map[string]bool{}
	for _, cfg := range a.cfg.Host.FileRoots {
		id := strings.TrimSpace(cfg.ID)
		if id == "" || seen[id] {
			continue
		}
		path, err := a.normalizeHostRootPath(cfg.Path)
		if err != nil {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(cfg.Label)
		if label == "" {
			label = id
		}
		roots = append(roots, hostFileRoot{ID: id, Label: label, Path: path, Download: cfg.Download, Source: "config"})
	}
	if a.store != nil {
		savedRoots, err := a.store.ListHostFileRoots()
		if err == nil {
			for _, saved := range savedRoots {
				if strings.TrimSpace(saved.RootID) == "" || seen[saved.RootID] {
					continue
				}
				path, err := a.normalizeHostRootPath(saved.Path)
				if err != nil {
					continue
				}
				if hostRootPathAlreadyListed(roots, path) {
					continue
				}
				saved.Path = path
				roots = append(roots, hostRootFromSaved(saved))
				seen[saved.RootID] = true
			}
		}
	}
	return roots
}

func hostRootFromSaved(saved model.HostSavedFileRoot) hostFileRoot {
	label := strings.TrimSpace(saved.Label)
	if label == "" {
		label = hostDefaultLabelForPath(saved.Path)
	}
	return hostFileRoot{
		ID:        saved.RootID,
		Label:     label,
		Path:      saved.Path,
		Download:  saved.Download,
		Source:    "custom",
		Removable: true,
	}
}

func hostRootPathAlreadyListed(roots []hostFileRoot, path string) bool {
	for _, root := range roots {
		if root.Path == path {
			return true
		}
	}
	return false
}

func hostFileRootForPath(roots []hostFileRoot, currentRootID string, path string) hostFileRoot {
	path = filepath.Clean(path)
	for _, root := range roots {
		if root.ID == currentRootID {
			continue
		}
		if filepath.Clean(root.Path) == path {
			return root
		}
	}
	return hostFileRoot{}
}

func hostDefaultLabelForPath(path string) string {
	label := strings.TrimSpace(filepath.Base(path))
	if label == "" || label == "." || label == string(filepath.Separator) {
		return path
	}
	return label
}

func (a *App) resolveHostFilePath(r *http.Request) (hostFileRoot, string, string, error) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	roots := a.hostFileRoots()
	if rootID == "" {
		if len(roots) == 0 {
			return hostFileRoot{}, "", "", fmt.Errorf("no host file roots configured")
		}
		rootID = roots[0].ID
	}
	var root hostFileRoot
	for _, candidate := range roots {
		if candidate.ID == rootID {
			root = candidate
			break
		}
	}
	if root.ID == "" {
		return hostFileRoot{}, "", "", fmt.Errorf("unknown file root")
	}
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" || relPath == "." || relPath == "/" {
		relPath = "."
	}
	if filepath.IsAbs(relPath) || strings.ContainsAny(relPath, "\x00") {
		return hostFileRoot{}, "", "", fmt.Errorf("path must be relative")
	}
	relPath = filepath.Clean(filepath.FromSlash(relPath))
	if relPath == "." {
		relPath = ""
	}
	if !a.cfg.Host.ShowHidden && hostPathHasHiddenSegment(relPath) {
		return hostFileRoot{}, "", "", fmt.Errorf("hidden paths are not exposed")
	}
	absPath := filepath.Clean(filepath.Join(root.Path, relPath))
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	if !a.pathInside(absPath, root.Path) {
		return hostFileRoot{}, "", "", fmt.Errorf("path is outside file root")
	}
	return root, absPath, relPath, nil
}

func (a *App) normalizeHostRootPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("file root path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(a.cfg.Shell.ManifestPath), path)
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("file root is not a directory")
	}
	return path, nil
}

func hostDiskForRoot(root hostFileRoot) (hostDisk, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root.Path, &stat); err != nil {
		return hostDisk{}, false
	}
	blockSize := int64(stat.Bsize)
	total := int64(stat.Blocks) * blockSize
	available := int64(stat.Bavail) * blockSize
	used := total - available
	return hostDisk{
		RootID:         root.ID,
		Label:          root.Label,
		Path:           root.Path,
		TotalBytes:     total,
		AvailableBytes: available,
		UsedBytes:      used,
		UsedPercent:    percent(used, total),
	}, true
}

func hostMemoryOverview() hostMemory {
	values := readProcMeminfo()
	total := values["MemTotal"] * 1024
	available := values["MemAvailable"] * 1024
	if available == 0 {
		available = values["MemFree"] * 1024
	}
	used := total - available
	if used < 0 {
		used = 0
	}
	return hostMemory{TotalBytes: total, AvailableBytes: available, UsedBytes: used, UsedPercent: percent(used, total)}
}

func readProcMeminfo() map[string]int64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return map[string]int64{}
	}
	defer file.Close()
	values := map[string]int64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSuffix(scanner.Text(), ":"))
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		values[key] = value
	}
	return values
}

func hostLoadAverage() []float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return []float64{}
	}
	fields := strings.Fields(string(data))
	out := make([]float64, 0, 3)
	for index := 0; index < len(fields) && index < 3; index++ {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err == nil {
			out = append(out, value)
		}
	}
	return out
}

func hostUptimeSeconds() int64 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err == nil {
		return int64(info.Uptime)
	}
	return 0
}

func (a *App) fileWithinHostDownloadLimit(size int64) bool {
	limit := a.hostMaxDownloadBytes()
	return limit <= 0 || size <= limit
}

func (a *App) hostMaxDownloadBytes() int64 {
	if a.cfg.Host.MaxDownloadBytes <= 0 {
		return 512 << 20
	}
	return a.cfg.Host.MaxDownloadBytes
}

func hostPathHasHiddenSegment(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func fileSizeForKind(kind string, size int64) int64 {
	if kind == "file" {
		return size
	}
	return 0
}

func safeDownloadFilename(value string) string {
	return strings.NewReplacer(`"`, "_", "\r", "_", "\n", "_").Replace(value)
}

func firstFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}

func percent(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return clampPercent(float64(used) / float64(total) * 100)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
