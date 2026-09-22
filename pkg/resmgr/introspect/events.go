// Copyright The NRI Plugins Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package introspect

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// maxSubscribers caps concurrent event subscribers, so that the
	// endpoint cannot be used to pin an unbounded number of goroutines
	// and buffers.
	maxSubscribers = 16
	// heartbeatInterval is how often a comment is written to an idle
	// stream. Without it, kubectl port-forward and intervening proxies
	// eventually reap the connection. Server.heartbeat overrides it, which
	// is how a test reaches this path without waiting half a minute.
	heartbeatInterval = 30 * time.Second
)

// The subscriber channels' whole lifetime lives in this file — created by
// subscribe, delivered to by notifySubscribers, closed by closeSubscribers,
// removed by unsubscribe — so that the one invariant binding them is
// stateable in one place: every one of those operations runs under s.mu, so
// a close can never race a send.

// subscribe registers an event subscriber, returning false if we are full
// or no longer enabled.
func (s *Server) subscribe() (chan uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Checking enabled matters: pkg/http dispatches with its lock
	// released and Unregister does not wait for requests in flight, so a
	// request admitted just before Disable() reaches its handler body
	// afterwards. Without this it would insert a channel into the map
	// Disable() has already emptied, and nothing would ever close it —
	// a goroutine and a cap slot leaked until the client disconnects.
	if !s.enabled {
		return nil, false
	}

	if len(s.subs) >= maxSubscribers {
		return nil, false
	}

	// Buffered for one value: delivery never blocks, and a subscriber
	// refetches the current snapshot anyway, so a coalesced older
	// generation loses nothing.
	ch := make(chan uint64, 1)
	s.subs[ch] = struct{}{}

	return ch, true
}

// unsubscribe removes an event subscriber.
func (s *Server) unsubscribe(ch chan uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
	}
}

// subscribers returns the number of current event subscribers.
func (s *Server) subscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.subs)
}

// notifySubscribers delivers a generation to every subscriber without ever
// blocking. Each channel is buffered for one value: if a notification is
// already pending the subscriber has not read it yet, and since it refetches
// the current snapshot anyway, an older pending value loses nothing.
// Coalescing is guaranteed by the buffer: a subscriber not reading has at
// most one notification pending, never a queue. Do not try to improve this
// by draining and replacing; the handler can read between the sends anyway,
// and which generation is pending does not matter — the client refetches the
// current snapshot regardless.
//
// Callers must hold s.mu. That is what keeps a send from racing a close.
func (s *Server) notifySubscribers(gen uint64) {
	for ch := range s.subs {
		select {
		case ch <- gen:
		default:
		}
	}
}

// closeSubscribers ends every event stream. Closing is the only thing that
// does: unregistering the handler does not, because pkg/http does not wait
// for requests in flight.
//
// Callers must hold s.mu.
func (s *Server) closeSubscribers() {
	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}
}

// serveEvents streams change notifications as Server-Sent Events.
//
// Each event carries only a generation number; the client refetches the
// snapshot. That way the server marshals nothing until someone asks, and a
// slow client cannot make a producer wait.
func (s *Server) serveEvents(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, ok := s.subscribe()
	if !ok {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer s.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Read under the mutex like every other field of Server, even though
	// only a test ever writes it.
	s.mu.Lock()
	interval := s.heartbeat
	s.mu.Unlock()

	if interval <= 0 {
		interval = heartbeatInterval
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()

	for {
		select {
		case <-req.Context().Done():
			return

		case gen, ok := <-ch:
			if !ok {
				// Disable() closed us.
				return
			}
			if _, err := fmt.Fprintf(w, "data: {\"generation\":%d}\n\n", gen); err != nil {
				log.Debugf("failed to write event: %v", err)
				return
			}
			flusher.Flush()

		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				log.Debugf("failed to write heartbeat: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}
