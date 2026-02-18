package session

import "sync"

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan Event]struct{}{}}
}

func (h *Hub) Subscribe(sessionID string, buf int) (<-chan Event, func()) {
	ch := make(chan Event, buf)
	h.mu.Lock()
	if _, ok := h.subs[sessionID]; !ok {
		h.subs[sessionID] = map[chan Event]struct{}{}
	}
	h.subs[sessionID][ch] = struct{}{}
	h.mu.Unlock()
	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if sessionSubs, ok := h.subs[sessionID]; ok {
			delete(sessionSubs, ch)
			close(ch)
			if len(sessionSubs) == 0 {
				delete(h.subs, sessionID)
			}
		}
	}
	return ch, unsub
}

func (h *Hub) Publish(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[ev.SessionID] {
		select {
		case ch <- ev:
		default:
		}
	}
}
