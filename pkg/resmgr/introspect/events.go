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
	// eventually reap the connection.
	heartbeatInterval = 30 * time.Second
)

// subscribe registers an event subscriber, returning false if we are full.
func (s *Server) subscribe() (chan uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.subs) >= maxSubscribers {
		return nil, false
	}

	// Buffered for one value: notify() never blocks, and a subscriber
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

// serveEvents streams change notifications as Server-Sent Events.
//
// Each event carries only a generation number; the client refetches the
// snapshot. That way the server marshals nothing until someone asks, and a
// slow client cannot make a producer wait.
func (s *Server) serveEvents(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, ok := s.subscribe()
	if !ok {
		http.Error(w, "too many subscribers", http.StatusServiceUnavailable)
		return
	}
	defer s.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatInterval)
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
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				log.Debugf("failed to write heartbeat: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}
