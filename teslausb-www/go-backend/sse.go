package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Broker polls a Collector on a fixed interval and fans each Status snapshot
// out to all active SSE subscribers.
type Broker struct {
	collector *Collector
	interval  time.Duration

	mu   sync.Mutex
	subs map[chan Status]struct{}
	last Status
}

func NewBroker(col *Collector, interval time.Duration) *Broker {
	return &Broker{
		collector: col,
		interval:  interval,
		subs:      make(map[chan Status]struct{}),
	}
}

// Run starts the polling loop; blocks until ctx is cancelled.
func (b *Broker) Run(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	b.publish(b.collector.Collect()) // immediate first snapshot
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.publish(b.collector.Collect())
		}
	}
}

func (b *Broker) publish(s Status) {
	b.mu.Lock()
	b.last = s
	subs := make([]chan Status, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- s:
		default:
			// Slow consumer — drop rather than block the broker.
		}
	}
}

func (b *Broker) subscribe() (ch chan Status, last Status) {
	ch = make(chan Status, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	last = b.last
	b.mu.Unlock()
	return
}

func (b *Broker) unsubscribe(ch chan Status) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// ServeSnapshot handles GET /api/status — returns a single JSON snapshot.
func (b *Broker) ServeSnapshot(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	last := b.last
	b.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(last); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}

// ServeSSE handles GET /api/status/stream as a text/event-stream response.
func (b *Broker) ServeSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // tell nginx not to buffer the stream
	w.WriteHeader(http.StatusOK)

	ch, last := b.subscribe()
	defer b.unsubscribe(ch)

	// Send the current snapshot immediately so the client doesn't wait one poll.
	if err := writeEvent(w, last); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case s := <-ch:
			if err := writeEvent(w, s); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeEvent(w http.ResponseWriter, s Status) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}
