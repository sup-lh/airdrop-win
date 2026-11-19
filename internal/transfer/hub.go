package transfer

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ClientInfo struct {
	ID          string    `json:"id"`
	Role        string    `json:"role"` // "pc" or "remote"
	DeviceName  string    `json:"deviceName"`
	Platform    string    `json:"platform"` // "ios", "macos", "android", "windows", "web"
	IP          string    `json:"ip"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Client struct {
	Info ClientInfo
	conn *websocket.Conn
	send chan []byte
	hub  *Hub
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
	}
}

func (h *Hub) Register(conn *websocket.Conn, info ClientInfo) *Client {
	if info.ID == "" {
		info.ID = NewID()
	}
	if info.ConnectedAt.IsZero() {
		info.ConnectedAt = time.Now()
	}
	c := &Client{
		Info: info,
		conn: conn,
		send: make(chan []byte, 64),
		hub:  h,
	}

	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	go c.writePump()
	h.BroadcastDevices()
	return c
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	_, ok := h.clients[c]
	if ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
	if ok {
		h.BroadcastDevices()
	}
}

func (h *Hub) UpdateDeviceName(c *Client, name string) {
	if name == "" {
		return
	}
	h.mu.Lock()
	c.Info.DeviceName = name
	h.mu.Unlock()
	h.BroadcastDevices()
}

func (h *Hub) Devices() []ClientInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ClientInfo, 0, len(h.clients))
	for c := range h.clients {
		if c.Info.Role == "remote" {
			out = append(out, c.Info)
		}
	}
	return out
}

func (h *Hub) BroadcastDevices() {
	h.Broadcast(Event{
		Type: "devices",
		Data: h.Devices(),
	})
}

func (h *Hub) Broadcast(ev Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- payload:
		default:
		}
	}
}

func (h *Hub) BroadcastToRole(role string, ev Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.Info.Role == role {
			select {
			case c.send <- payload:
			default:
			}
		}
	}
}

// KickRemotes closes all remote WebSocket connections (e.g. when pairing token is reset).
func (h *Hub) KickRemotes() {
	h.mu.Lock()
	var toKick []*Client
	for c := range h.clients {
		if c.Info.Role == "remote" {
			toKick = append(toKick, c)
		}
	}
	h.mu.Unlock()
	for _, c := range toKick {
		_ = c.conn.Close()
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(64 * 1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		var req struct {
			Type       string `json:"type"`
			DeviceName string `json:"deviceName"`
		}
		if json.Unmarshal(msg, &req) == nil {
			switch req.Type {
			case "rename":
				c.hub.UpdateDeviceName(c, req.DeviceName)
			case "ping":
				_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
