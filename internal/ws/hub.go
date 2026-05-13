package ws

import (
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*websocket.Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*websocket.Conn]struct{})}
}

func (h *Hub) Add(roomID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.rooms[roomID] == nil {
		h.rooms[roomID] = make(map[*websocket.Conn]struct{})
	}
	h.rooms[roomID][conn] = struct{}{}
}

func (h *Hub) Remove(roomID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.rooms[roomID], conn)
	if len(h.rooms[roomID]) == 0 {
		delete(h.rooms, roomID)
	}
}

func (h *Hub) Broadcast(roomID, event string, payload any) {
	message, _ := json.Marshal(map[string]any{
		"event": event,
		"data":  payload,
	})

	h.mu.RLock()
	connections := make([]*websocket.Conn, 0, len(h.rooms[roomID]))
	for conn := range h.rooms[roomID] {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	for _, conn := range connections {
		_ = conn.WriteMessage(websocket.TextMessage, message)
	}
}

func (h *Hub) CloseRoom(roomID string) {
	h.Broadcast(roomID, "room_closed", map[string]string{"room_id": roomID})

	h.mu.Lock()
	connections := h.rooms[roomID]
	delete(h.rooms, roomID)
	h.mu.Unlock()

	for conn := range connections {
		_ = conn.Close()
	}
}
