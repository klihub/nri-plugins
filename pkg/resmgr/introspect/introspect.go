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
	"encoding/json"
	"net/http"
	"sync"

	xhttp "github.com/containers/nri-plugins/pkg/http"
	"github.com/containers/nri-plugins/pkg/introspect/api"
)

const (
	// SnapshotPath serves the node state snapshot.
	SnapshotPath = "/introspect"
	// EventsPath serves change notifications.
	EventsPath = "/introspect/events"
)

// Snapshot aliases the wire format's snapshot type, so that consumers of
// this package need not import the api package alongside it. That matters in
// pkg/resmgr, where the identifier `api` already means containerd's NRI api.
type Snapshot = api.Snapshot

// CollectFn returns a fresh snapshot. It must be safe to call from an HTTP
// handler goroutine — the resource manager's implementation takes its read
// lock — and it must return a snapshot the server may mutate and retain, not
// a shared or pooled one: the server stamps the generation into it.
type CollectFn func() *Snapshot

// Server serves the introspection interface.
//
// The snapshot is built lazily and cached per generation, so with nobody
// asking, nothing is ever collected or marshalled. Invalidate() only bumps
// a counter and wakes subscribers.
type Server struct {
	mu         sync.Mutex
	mux        *xhttp.ServeMux
	collect    CollectFn
	enabled    bool
	generation uint64
	cachedGen  uint64
	cached     []byte
	subs       map[chan uint64]struct{}
}

// New creates a server. It registers no handlers until Enable is called.
func New(mux *xhttp.ServeMux, collect CollectFn) *Server {
	return &Server{
		mux:        mux,
		collect:    collect,
		generation: 1,
		subs:       map[chan uint64]struct{}{},
	}
}

// Enable starts serving the introspection endpoints. It is idempotent.
//
// The mux is touched with our mutex released, for the same reason collect()
// is: registering takes the mux' write lock, which waits for the requests
// in flight, and a request in flight needs this mutex to finish. Holding
// both closes that cycle. Enable and Disable are called from the resource
// manager's configuration paths, which serialize them.
func (s *Server) Enable() {
	s.mu.Lock()
	if s.enabled {
		s.mu.Unlock()
		return
	}
	s.enabled = true
	s.mu.Unlock()

	log.Infof("enabling introspection interface...")

	s.mux.HandleFunc(SnapshotPath, s.serveSnapshot)
	s.mux.HandleFunc(EventsPath, s.serveEvents)
}

// Disable stops serving and drops any subscribers. It is idempotent.
//
// Subscribers are woken before the handler is unregistered, and the mux is
// touched with our mutex released: see Enable().
func (s *Server) Disable() {
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return
	}

	log.Infof("disabling introspection interface...")

	s.enabled = false
	s.cached, s.cachedGen = nil, 0

	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}

	s.mu.Unlock()

	s.mux.Unregister(SnapshotPath)
	s.mux.Unregister(EventsPath)
}

// Invalidate marks the cached snapshot stale and wakes subscribers.
//
// This is called from NRI event handlers while they hold the resource
// manager's write lock, so it must never block on anything that needs that
// lock. It takes this server's mutex briefly and sends without blocking.
func (s *Server) Invalidate() {
	s.mu.Lock()

	s.generation++
	gen := s.generation

	subs := make([]chan uint64, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}

	s.mu.Unlock()

	for _, ch := range subs {
		notify(ch, gen)
	}
}

// notify delivers a generation without ever blocking. The channel is
// buffered for one value: if a notification is already pending the
// subscriber has not read it yet, and since it refetches the current
// snapshot anyway, an older pending value loses nothing.
func notify(ch chan uint64, gen uint64) {
	select {
	case ch <- gen:
	default:
	}
}

// snapshot returns the marshalled snapshot for the current generation,
// collecting it if the cache is stale.
//
// collect() is deliberately called with the mutex released: it takes the
// resource manager's read lock, and Invalidate() runs while that lock is
// held for writing. Holding this mutex across collect() would deadlock.
//
// The generation is sampled before collecting, so a body may reflect state
// newer than its stamp claims — never older. That direction is the safe one:
// a client told generation N never concludes it is current when it is not.
// The cost is that a collection racing an invalidation is cached under an
// already-superseded generation and will never be hit, so on a node changing
// constantly the cache simply stops helping.
//
// The returned slice is shared by every concurrent request. It is only ever
// replaced, never appended to or written through, which is what makes that
// safe — do not "optimise" it into a reused buffer.
func (s *Server) snapshot() ([]byte, error) {
	s.mu.Lock()
	gen := s.generation
	if s.cached != nil && s.cachedGen == gen {
		data := s.cached
		s.mu.Unlock()
		return data, nil
	}
	collect := s.collect
	s.mu.Unlock()

	snap := collect()
	if snap == nil {
		snap = &api.Snapshot{Version: api.Version}
	}
	snap.Generation = gen

	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	// Another request may have finished a newer collection meanwhile; keep
	// that one. A completing collection always stores under its own
	// generation, never a newer one, so this cannot cache a stale body
	// under a newer stamp.
	if s.cachedGen < gen {
		s.cached, s.cachedGen = data, gen
	}
	s.mu.Unlock()

	return data, nil
}

// serveSnapshot serves the node state snapshot.
func (s *Server) serveSnapshot(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data, err := s.snapshot()
	if err != nil {
		log.Errorf("failed to produce snapshot: %v", err)
		http.Error(w, "failed to produce snapshot", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(data); err != nil {
		log.Debugf("failed to write snapshot response: %v", err)
	}
}
