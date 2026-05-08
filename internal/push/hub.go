package push

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

// WSHub maintains active WebSocket connections per device.
type WSHub struct {
	mu          sync.RWMutex
	connections map[string]*wsConn
}

type wsConn struct {
	conn     *websocket.Conn
	deviceID string
	id       uint64
	cancel   context.CancelFunc
	writeMu  sync.Mutex
}

// WSMessage is the JSON envelope sent to clients.
type WSMessage struct {
	Type   string `json:"type"`
	Stream string `json:"stream,omitempty"`
	Action string `json:"action,omitempty"`
	Ts     int64  `json:"ts,omitempty"`
}

const (
	wsWriteTimeout = 5 * time.Second
	wsPingInterval = 30 * time.Second
)

var wsConnID uint64

func NewWSHub() *WSHub {
	return &WSHub{
		connections: make(map[string]*wsConn),
	}
}

// Register adds a WebSocket connection for a device, replacing any existing one.
func (h *WSHub) Register(deviceID string, conn *websocket.Conn, cancel context.CancelFunc) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.connections[deviceID]; ok {
		existing.cancel()
		_ = existing.conn.Close(websocket.StatusNormalClosure, "replaced")
		log.Printf("ws hub: replaced existing connection for device %s", deviceID)
	}

	connID := atomic.AddUint64(&wsConnID, 1)
	h.connections[deviceID] = &wsConn{
		conn:     conn,
		deviceID: deviceID,
		id:       connID,
		cancel:   cancel,
	}
	log.Printf("ws hub: registered device %s (total: %d)", deviceID, len(h.connections))
	return connID
}

// Unregister removes a WebSocket connection only if it is still the active one.
func (h *WSHub) Unregister(deviceID string, connID uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.connections[deviceID]; ok {
		if connID != 0 && c.id != connID {
			log.Printf("ws hub: ignored stale unregister for device %s", deviceID)
			return
		}
		c.cancel()
		delete(h.connections, deviceID)
		log.Printf("ws hub: unregistered device %s (total: %d)", deviceID, len(h.connections))
	}
}

// Send pushes a notification to a specific device's WebSocket if connected.
func (h *WSHub) Send(deviceID string, notification PushNotification) bool {
	h.mu.RLock()
	c, ok := h.connections[deviceID]
	h.mu.RUnlock()
	if !ok {
		return false
	}

	msg := WSMessage{
		Type:   "push",
		Stream: notification.Stream,
		Action: notification.Action,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return false
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		log.Printf("ws hub: write to device %s failed: %v", deviceID, err)
		return false
	}
	return true
}

// IsConnected checks if a device has an active WebSocket connection.
func (h *WSHub) IsConnected(deviceID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.connections[deviceID]
	return ok
}

// CloseAll shuts down every active connection.
func (h *WSHub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.connections {
		c.cancel()
		_ = c.conn.Close(websocket.StatusGoingAway, "server shutting down")
		delete(h.connections, id)
	}
	log.Printf("ws hub: closed all connections")
}

// Serve runs read/write pumps for a connection. Blocks until done.
func (h *WSHub) Serve(ctx context.Context, deviceID string, conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	connID := h.Register(deviceID, conn, cancel)

	// Send welcome message
	welcome := WSMessage{Type: "connected", Ts: time.Now().Unix()}
	welcomeData, _ := json.Marshal(welcome)
	writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
	_ = conn.Write(writeCtx, websocket.MessageText, welcomeData)
	writeCancel()

	go h.writePump(ctx, deviceID, conn, connID)
	h.readPump(ctx, deviceID, conn, connID)
}

// readPump reads from the WebSocket to detect client disconnects.
func (h *WSHub) readPump(ctx context.Context, deviceID string, conn *websocket.Conn, connID uint64) {
	defer h.Unregister(deviceID, connID)
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			log.Printf("ws hub: device %s disconnected: %v", deviceID, err)
			return
		}
	}
}

// writePump sends periodic ping messages to keep the connection alive.
func (h *WSHub) writePump(ctx context.Context, deviceID string, conn *websocket.Conn, connID uint64) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.RLock()
			c, ok := h.connections[deviceID]
			h.mu.RUnlock()
			if !ok || c.id != connID {
				return
			}
			ping := WSMessage{Type: "ping", Ts: time.Now().Unix()}
			data, _ := json.Marshal(ping)
			c.writeMu.Lock()
			writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			c.writeMu.Unlock()
			if err != nil {
				log.Printf("ws hub: ping to device %s failed: %v", deviceID, err)
				return
			}
		}
	}
}
