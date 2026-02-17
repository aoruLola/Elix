package run

import (
	"sync"

	"echohelix/internal/events"
)

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan events.Event]struct{}
}

func NewHub() *Hub {
	return &Hub{
		subs: map[string]map[chan events.Event]struct{}{},
	}
}

func (h *Hub) Subscribe(runID string, buf int) (<-chan events.Event, func()) {
	ch := make(chan events.Event, buf)
	h.mu.Lock()
	if _, ok := h.subs[runID]; !ok {
		h.subs[runID] = map[chan events.Event]struct{}{}
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if runSubs, ok := h.subs[runID]; ok {
			delete(runSubs, ch)
			close(ch)
			if len(runSubs) == 0 {
				delete(h.subs, runID)
			}
		}
	}
	return ch, unsub
}

func (h *Hub) Publish(ev events.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[ev.RunID] {
		select {
		case ch <- ev:
		default:
		}
	}
}
