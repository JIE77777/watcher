package push

import (
	"context"
	"log"
	"strings"

	"watcher/internal/model"
)

const (
	StatusDisabled  = "disabled"
	StatusReserved  = "reserved"
	StatusReady     = "ready"
	StatusConnected = "connected"
	StatusError     = "error"
)

type RelayPushPublisher interface {
	PublishEnvelope(ctx context.Context, envelope model.EventEnvelope) error
	NotifyPush(ctx context.Context, stream string) error
}

type ChannelStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ManagerStatus struct {
	Channels []ChannelStatus `json:"channels"`
}

type Manager struct {
	cfg       Config
	relayPush RelayPushPublisher
}

func NewManager(cfg Config, relayPush RelayPushPublisher) *Manager {
	return &Manager{
		cfg:       cfg,
		relayPush: relayPush,
	}
}

func (m *Manager) Status() ManagerStatus {
	if m == nil {
		return ManagerStatus{}
	}
	channels := []ChannelStatus{
		{Name: ProviderSelfHost, Status: StatusReserved},
		{Name: ProviderFCM, Status: StatusReserved},
		{Name: ProviderAPNs, Status: StatusReserved},
		{Name: ProviderHuaWei, Status: StatusReserved},
	}
	if m.relayPush != nil {
		channels = append([]ChannelStatus{{Name: "relay_push", Status: StatusReady}}, channels...)
	} else {
		channels = append([]ChannelStatus{{Name: "relay_push", Status: StatusDisabled}}, channels...)
	}
	xiaomiStatus := StatusDisabled
	if strings.TrimSpace(m.cfg.Xiaomi.AppID) != "" && strings.TrimSpace(m.cfg.Xiaomi.AppSecret) != "" {
		xiaomiStatus = StatusReady
	}
	channels = append(channels, ChannelStatus{Name: ProviderXiaomi, Status: xiaomiStatus})
	return ManagerStatus{Channels: channels}
}

func (m *Manager) PublishRelayPushEnvelope(ctx context.Context, envelope model.EventEnvelope) {
	if m == nil || m.relayPush == nil {
		return
	}
	if err := m.relayPush.PublishEnvelope(ctx, envelope); err != nil {
		log.Printf("relay push publish failed: %v", err)
	}
}

func (m *Manager) NotifyRelayPush(ctx context.Context, stream string) {
	if m == nil || m.relayPush == nil {
		return
	}
	if err := m.relayPush.NotifyPush(ctx, stream); err != nil {
		log.Printf("relay push notify: %v", err)
	}
}
