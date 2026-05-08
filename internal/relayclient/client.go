package relayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"watcher/internal/model"
	"watcher/internal/netpolicy"
)

type Client struct {
	BaseURL    string
	OwnerToken string
	HTTPClient *http.Client
}

func (c Client) PublishEnvelope(ctx context.Context, envelope model.EventEnvelope) error {
	if c.BaseURL == "" || c.OwnerToken == "" {
		return fmt.Errorf("relay client is not configured")
	}
	client := c.HTTPClient
	if client == nil {
		client = netpolicy.DirectHTTPClient(0)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/api/v2/events/publish"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.OwnerToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("relay envelope publish failed with status %s", resp.Status)
	}
	return nil
}

// NotifyPush sends a push notification wake-up to relay for a specific event stream.
func (c Client) NotifyPush(ctx context.Context, stream string) error {
	if c.BaseURL == "" || c.OwnerToken == "" {
		return fmt.Errorf("relay client is not configured")
	}
	client := c.HTTPClient
	if client == nil {
		client = netpolicy.DirectHTTPClient(0)
	}
	body, _ := json.Marshal(map[string]string{
		"stream": stream,
		"action": "sync",
	})
	url := strings.TrimRight(c.BaseURL, "/") + "/api/v2/push/notify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.OwnerToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("relay push notify failed with status %s", resp.Status)
	}
	return nil
}
