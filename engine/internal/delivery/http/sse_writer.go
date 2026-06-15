package http

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEWriter writes Server-Sent Events to an http.ResponseWriter. Writes are
// serialized by mu so the heartbeat goroutine (StartHeartbeat) and the request
// goroutine's event writes never touch the underlying writer concurrently —
// concurrent writes to an http.ResponseWriter are not safe.
type SSEWriter struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates an SSEWriter after setting SSE headers.
// Returns an error if the ResponseWriter does not support flushing.
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	return &SSEWriter{w: w, flusher: flusher}, nil
}

// WriteEvent writes a single SSE event with the given type and data.
func (s *SSEWriter) WriteEvent(eventType string, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", eventType, data)
	if err != nil {
		return fmt.Errorf("write SSE event: %w", err)
	}
	s.flusher.Flush()
	return nil
}

// WriteComment writes an SSE comment line (prefixed with ':').
func (s *SSEWriter) WriteComment(comment string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, ": %s\n\n", comment)
	s.flusher.Flush()
}

// StartHeartbeat sends comment heartbeats at the given interval. It returns a
// stop function that closes the goroutine AND blocks until it has fully exited,
// so once stop returns no further write to the underlying writer can occur —
// the caller may then safely read or close it. stop is idempotent.
func (s *SSEWriter) StartHeartbeat(interval time.Duration) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.WriteComment("heartbeat")
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}
