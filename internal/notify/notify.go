package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"time"

	"watcher/internal/model"
	"watcher/internal/netpolicy"
)

func Desktop(event model.WatcherTaskEvent) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil
	}
	if os.Getenv("DISPLAY") == "" &&
		os.Getenv("WAYLAND_DISPLAY") == "" &&
		os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "notify-send", "-a", "Watcher", event.DisplayTitle(), event.Summary)
	_ = cmd.Run()
	return nil
}

func Webhook(ctx context.Context, url string, headers map[string]string, event model.WatcherTaskEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := netpolicy.DirectHTTPClient(0).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
