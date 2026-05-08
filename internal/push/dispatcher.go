package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"watcher/internal/netpolicy"
)

// ── Provider constants ────────────────────────────────────────────────

const (
	ProviderXiaomi   = "xiaomi"
	ProviderFCM      = "fcm"      // reserved — Google Play Services
	ProviderAPNs     = "apns"     // reserved — Apple Push Notification Service
	ProviderHuaWei   = "huawei"   // reserved — Huawei Push Kit
	ProviderSelfHost = "selfhost" // reserved — WebSocket / SSE self-hosted channel

	miPushAPIURL      = "https://api.xmpush.xiaomi.com/v3/message/regid"
	miPushSandboxURL  = "https://sandbox.xmpush.xiaomi.com/v3/message/regid"
	xiaomiSuccessCode = 0
	httpTimeout       = 8 * time.Second
)

// ── Provider configs ──────────────────────────────────────────────────

// XiaomiConfig holds credentials for Xiaomi Push (MiPush).
type XiaomiConfig struct {
	AppID      string `json:"app_id"`
	AppKey     string `json:"app_key"`
	AppSecret  string `json:"app_secret"`
	UseSandbox bool   `json:"use_sandbox"`
	ChannelID  string `json:"channel_id,omitempty"` // optional notification channel
}

// FCMConfig holds credentials for Firebase Cloud Messaging.
// Reserved: not yet implemented.
type FCMConfig struct {
	ProjectID       string `json:"project_id"`
	ServiceAccount  string `json:"service_account_json_path"`
}

// APNsConfig holds credentials for Apple Push Notification service.
// Reserved: not yet implemented.
type APNsConfig struct {
	TeamID    string `json:"team_id"`
	KeyID     string `json:"key_id"`
	BundleID  string `json:"bundle_id"`
	KeyFile   string `json:"key_file"`   // .p8 private key path
	UseSandbox bool  `json:"use_sandbox"`
}

// HuaWeiConfig holds credentials for Huawei Push Kit.
// Reserved: not yet implemented.
type HuaWeiConfig struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

// Config holds all push provider configurations.
type Config struct {
	Xiaomi XiaomiConfig `json:"xiaomi"`
	FCM    FCMConfig     `json:"fcm"`    // reserved
	APNs   APNsConfig    `json:"apns"`   // reserved
	HuaWei HuaWeiConfig  `json:"huawei"` // reserved
}

// AnyConfigured reports whether any push provider has at least the
// minimum credentials set. The selfhost (WebSocket) channel is always
// available and is not gated by this check.
func (c Config) AnyConfigured() bool {
	return c.Xiaomi.AppID != "" && c.Xiaomi.AppSecret != "" ||
		c.FCM.ProjectID != "" ||
		c.APNs.TeamID != "" ||
		c.HuaWei.AppID != ""
}

// ── Push notification ─────────────────────────────────────────────────

// PushNotification is a lightweight wake-up payload sent to a device.
type PushNotification struct {
	Stream string `json:"stream"`
	Action string `json:"action"`
}

// DevicePushInfo carries push routing information for a device.
type DevicePushInfo struct {
	DeviceID     string `json:"device_id"`
	PushProvider string `json:"push_provider,omitempty"`
	PushToken    string `json:"push_token,omitempty"`
}

// ── Dispatcher ────────────────────────────────────────────────────────

// Dispatcher sends push notifications through provider-specific channels.
type Dispatcher struct {
	cfg    Config
	client *http.Client
	hub    *WSHub
}

// NewDispatcher creates a push dispatcher from configuration.
func NewDispatcher(cfg Config, hub *WSHub) *Dispatcher {
	return &Dispatcher{
		cfg:    cfg,
		client: netpolicy.DirectHTTPClient(httpTimeout),
		hub:    hub,
	}
}

// Dispatch sends a push notification to a single device.
// Returns nil on success, error on failure (caller decides whether to log/ignore).
func (d *Dispatcher) Dispatch(ctx context.Context, device DevicePushInfo, notification PushNotification) error {
	provider := strings.TrimSpace(device.PushProvider)
	if provider == "" {
		provider = GuessProvider(device.PushToken)
	}
	switch provider {
	case ProviderXiaomi:
		return d.dispatchXiaomi(ctx, device, notification)
	case ProviderFCM:
		// TODO: FCM dispatch for international devices
		return fmt.Errorf("fcm provider not yet implemented")
	case ProviderAPNs:
		// TODO: APNs dispatch for iOS
		return fmt.Errorf("apns provider not yet implemented")
	case ProviderHuaWei:
		// TODO: HuaWei Push Kit dispatch
		return fmt.Errorf("huawei provider not yet implemented")
	case ProviderSelfHost:
		if d.hub == nil {
			return fmt.Errorf("ws hub not initialized")
		}
		if !d.hub.Send(device.DeviceID, notification) {
			return fmt.Errorf("device %s not connected via websocket", device.DeviceID)
		}
		return nil
	default:
		return fmt.Errorf("unknown push provider %q", provider)
	}
}

// DispatchAll sends a push notification to all provided devices concurrently.
// Errors are logged per-device; the method returns a summary error if all failed.
func (d *Dispatcher) DispatchAll(ctx context.Context, devices []DevicePushInfo, notification PushNotification) error {
	if len(devices) == 0 {
		return nil
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures int
	)
	for _, device := range devices {
		device := device
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.Dispatch(ctx, device, notification); err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				log.Printf("push: dispatch to %s failed: %v", device.DeviceID, err)
			}
		}()
	}
	wg.Wait()
	if failures == len(devices) {
		return fmt.Errorf("push dispatch failed for all %d devices", failures)
	}
	if failures > 0 {
		log.Printf("push: %d/%d devices failed", failures, len(devices))
	}
	return nil
}

// GuessProvider infers the push provider from token format.
func GuessProvider(token string) string {
	if token == "" {
		return ""
	}
	if strings.HasPrefix(token, "ws:") {
		return ProviderSelfHost
	}
	return ProviderXiaomi
}

// --- Xiaomi Push (MiPush) ---

func (d *Dispatcher) dispatchXiaomi(ctx context.Context, device DevicePushInfo, notification PushNotification) error {
	xiaomi := d.cfg.Xiaomi
	if xiaomi.AppID == "" || xiaomi.AppKey == "" || xiaomi.AppSecret == "" {
		return fmt.Errorf("xiaomi push is not configured")
	}
	if strings.TrimSpace(device.PushToken) == "" {
		return fmt.Errorf("xiaomi reg_id is empty for device %s", device.DeviceID)
	}

	apiURL := miPushAPIURL
	if xiaomi.UseSandbox {
		apiURL = miPushSandboxURL
	}

	payload := buildXiaomiPayload(device, notification, xiaomi)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal xiaomi payload: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create xiaomi request: %w", err)
	}
	req.Header.Set("Authorization", "key="+xiaomi.AppSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("xiaomi push http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read xiaomi response: %w", err)
	}

	var result xiaomiPushResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse xiaomi response: %s", string(respBody))
	}
	if result.Code != xiaomiSuccessCode {
		return fmt.Errorf("xiaomi push failed: code=%d reason=%s description=%s",
			result.Code, result.Reason, result.Description)
	}

	log.Printf("push: xiaomi notification sent to device %s", device.DeviceID)
	return nil
}

type xiaomiPayload struct {
	Payload      string `json:"payload"`
	RegistrationID string `json:"registration_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	NotifyType   int    `json:"notify_type"`
	NotifyID     int    `json:"notify_id,omitempty"`
	ChannelID    string `json:"channel_id,omitempty"`
	Raw          int    `json:"raw"`
}

type xiaomiPushResponse struct {
	Code        int    `json:"code"`
	Reason      string `json:"reason"`
	Description string `json:"description"`
	Result      []struct {
		Code int    `json:"code"`
		Info string `json:"info"`
	} `json:"result,omitempty"`
}

func buildXiaomiPayload(device DevicePushInfo, notification PushNotification, cfg XiaomiConfig) xiaomiPayload {
	payloadJSON, _ := json.Marshal(notification)
	channelID := cfg.ChannelID
	if channelID == "" {
		channelID = "watcher_push"
	}
	return xiaomiPayload{
		Payload:        string(payloadJSON),
		RegistrationID: device.PushToken,
		Title:          "Watcher",
		Description:    "Shell 有新更新",
		NotifyType:     1,
		ChannelID:      channelID,
		Raw:            1,
	}
}
