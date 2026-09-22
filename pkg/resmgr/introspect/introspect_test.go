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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xhttp "github.com/containers/nri-plugins/pkg/http"
	"github.com/containers/nri-plugins/pkg/introspect/api"
)

func testSnapshot(node string) *api.Snapshot {
	return &api.Snapshot{
		Version: api.Version,
		Node:    api.Node{Name: node, Policy: "test"},
	}
}

func TestServeSnapshot(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, expected %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q, expected application/json", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control %q, expected no-store", cc)
	}

	snap := &api.Snapshot{}
	if err := json.Unmarshal(w.Body.Bytes(), snap); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if snap.Node.Name != "worker-1" {
		t.Errorf("unexpected node name %q", snap.Node.Name)
	}
	if snap.Generation != 1 {
		t.Errorf("generation %d, expected 1", snap.Generation)
	}
}

func TestDisabledRegistersNothing(t *testing.T) {
	mux := xhttp.NewServeMux()
	New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, expected %d for a server never enabled",
			w.Code, http.StatusNotFound)
	}
}

func TestDisableUnregisters(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })

	s.Enable()
	s.Disable()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, expected %d after Disable", w.Code, http.StatusNotFound)
	}

	// Enable must work again afterwards.
	s.Enable()
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status %d, expected %d after re-enabling", w.Code, http.StatusOK)
	}
}

func TestSnapshotIsCachedUntilInvalidated(t *testing.T) {
	var collected atomic.Int64

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		collected.Add(1)
		return testSnapshot("worker-1")
	})
	s.Enable()

	get := func() *api.Snapshot {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
		snap := &api.Snapshot{}
		if err := json.Unmarshal(w.Body.Bytes(), snap); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		return snap
	}

	first := get()
	second := get()

	if n := collected.Load(); n != 1 {
		t.Errorf("collected %d times for two requests, expected 1", n)
	}
	if first.Generation != second.Generation {
		t.Errorf("generation changed without an invalidation: %d then %d",
			first.Generation, second.Generation)
	}

	s.Invalidate()
	third := get()

	if n := collected.Load(); n != 2 {
		t.Errorf("collected %d times after invalidation, expected 2", n)
	}
	if third.Generation <= second.Generation {
		t.Errorf("generation %d did not advance past %d",
			third.Generation, second.Generation)
	}
}

// TestSlowCollectionDoesNotClobberNewer pins the overwrite guard in
// snapshot(): a collection that started at an older generation must not
// replace a body already cached at a newer one.
func TestSlowCollectionDoesNotClobberNewer(t *testing.T) {
	var (
		s       *Server
		release = make(chan struct{})
		entered = make(chan struct{})
		calls   atomic.Int64
	)

	mux := xhttp.NewServeMux()
	s = New(mux, func() *api.Snapshot {
		// Only the first collection blocks; it is the one that must
		// lose the race.
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return testSnapshot("slow")
		}
		return testSnapshot("fast")
	})
	s.Enable()

	get := func() *api.Snapshot {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
		snap := &api.Snapshot{}
		if err := json.Unmarshal(w.Body.Bytes(), snap); err != nil {
			t.Errorf("failed to unmarshal response: %v", err)
			return nil
		}
		return snap
	}

	slow := make(chan *api.Snapshot, 1)
	go func() { slow <- get() }()

	<-entered      // the slow collection holds generation 1
	s.Invalidate() // now at generation 2

	if fast := get(); fast == nil || fast.Node.Name != "fast" {
		t.Fatalf("expected the second request to collect afresh, got %+v", fast)
	}

	close(release)
	if got := <-slow; got == nil || got.Node.Name != "slow" {
		t.Errorf("the slow request should still see its own body, got %+v", got)
	}

	// The cached body must be the newer one, so a third request neither
	// re-collects nor serves the superseded generation.
	before := calls.Load()
	third := get()
	if third == nil || third.Node.Name != "fast" {
		t.Errorf("cache holds the superseded body: %+v", third)
	}
	if calls.Load() != before {
		t.Errorf("third request re-collected: %d calls, expected %d",
			calls.Load(), before)
	}
}

func TestNonGetIsRejected(t *testing.T) {
	var collected atomic.Int64

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		collected.Add(1)
		return testSnapshot("worker-1")
	})
	s.Enable()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/introspect", nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, expected %d for POST",
			w.Code, http.StatusMethodNotAllowed)
	}
	if allow := w.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("allow header %q, expected %q", allow, "GET, HEAD")
	}
	if n := collected.Load(); n != 0 {
		t.Errorf("collected %d times for a rejected request, expected 0", n)
	}
}

func TestNothingCollectedWithoutRequests(t *testing.T) {
	var collected atomic.Int64

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		collected.Add(1)
		return testSnapshot("worker-1")
	})
	s.Enable()

	for i := 0; i < 10; i++ {
		s.Invalidate()
	}

	if n := collected.Load(); n != 0 {
		t.Errorf("collected %d times with no request pending, expected 0", n)
	}
}

func TestInvalidateDoesNotBlockOnCollect(t *testing.T) {
	// Invalidate() is called from NRI handlers that hold the resource
	// manager's write lock, while collect() takes its read lock. If
	// Invalidate() could block behind a collect(), that is a deadlock.
	release := make(chan struct{})
	entered := make(chan struct{})

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		close(entered)
		<-release
		return testSnapshot("worker-1")
	})
	s.Enable()

	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
	}()

	<-entered // a collect() is now in flight

	done := make(chan struct{})
	go func() {
		s.Invalidate()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Invalidate() blocked while a collect() was in flight")
	}

	close(release)
}

func TestServeEvents(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	req := httptest.NewRequest(http.MethodGet, "/introspect/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	w := newSyncRecorder()

	served := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(served)
	}()

	// Wait until the subscriber is registered, then invalidate twice.
	for i := 0; i < 100; i++ {
		if s.subscribers() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.subscribers() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", s.subscribers())
	}

	s.Invalidate()
	s.Invalidate()

	// Wait for a record to be written rather than sleeping. After cancel()
	// both select arms are ready and Go picks between them at random, so
	// cancelling on a timer loses the event about half the time — a flake,
	// not a slow machine.
	w.waitForWrite(t)
	cancel()

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request was cancelled")
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q, expected text/event-stream", ct)
	}

	body := w.body()
	if !strings.Contains(body, "data: {\"generation\":") {
		t.Errorf("no generation event in the stream:\n%s", body)
	}

	// Coalescing: two invalidations arriving before the subscriber reads
	// must collapse into one record carrying the newer generation. Without
	// this the test would pass just as well with a 64-deep buffer, and the
	// design treats coalescing as a goal rather than an optimisation.
	if n := strings.Count(body, "data: "); n != 1 {
		t.Errorf("expected 1 coalesced record, got %d:\n%s", n, body)
	}
	if !strings.Contains(body, `data: {"generation":3}`) {
		t.Errorf("record does not carry the latest generation:\n%s", body)
	}

	if s.subscribers() != 0 {
		t.Errorf("subscriber not cleaned up: %d remain", s.subscribers())
	}
}

// syncRecorder is an http.ResponseWriter a test can read while the handler
// is still writing. httptest.ResponseRecorder cannot be read safely until
// the handler has returned, which is useless for a stream that only ends
// when the test says so.
type syncRecorder struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	hdr     http.Header
	code    int
	written chan struct{}
}

func newSyncRecorder() *syncRecorder {
	return &syncRecorder{
		hdr:     http.Header{},
		written: make(chan struct{}, 1),
	}
}

func (r *syncRecorder) Header() http.Header { return r.hdr }

func (r *syncRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.code = code
}

func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	n, err := r.buf.Write(p)
	r.mu.Unlock()

	select {
	case r.written <- struct{}{}:
	default:
	}

	return n, err
}

func (r *syncRecorder) Flush() {}

func (r *syncRecorder) body() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.buf.String()
}

// waitForWrite blocks until the handler has written something.
func (r *syncRecorder) waitForWrite(t *testing.T) {
	t.Helper()

	select {
	case <-r.written:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler wrote nothing")
	}
}

// TestEventsHeartbeat proves the idle-stream heartbeat, which is what keeps
// kubectl port-forward and intervening proxies from reaping a connection.
// The interval is shortened so the test does not wait half a minute.
func TestEventsHeartbeat(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.heartbeat = 10 * time.Millisecond
	s.Enable()

	req := httptest.NewRequest(http.MethodGet, "/introspect/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	w := newSyncRecorder()

	served := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(served)
	}()

	// No invalidation here, so the only thing that can be written is a
	// heartbeat.
	w.waitForWrite(t)
	cancel()

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request was cancelled")
	}

	if body := w.body(); !strings.Contains(body, ": heartbeat\n\n") {
		t.Errorf("no heartbeat comment in the stream:\n%q", body)
	}
}

// TestSubscribeRejectedWhenDisabled covers the window pkg/http leaves open:
// it dispatches with its lock released and Unregister does not wait for
// requests in flight, so a request admitted just before Disable() reaches
// its handler afterwards. It must not become a subscriber nobody will ever
// close.
func TestSubscribeRejectedWhenDisabled(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })

	s.Enable()
	s.Disable()

	if _, ok := s.subscribe(); ok {
		t.Error("subscribing succeeded while disabled")
	}
	if s.subscribers() != 0 {
		t.Errorf("a disabled server has %d subscribers", s.subscribers())
	}
}

// TestDisableTerminatesLiveStream pins that closing the subscriber channels
// is what ends a stream: since pkg/http no longer holds a lock across a
// handler, neither unregistering nor the server's write lock interrupts one.
//
// It does not pin the close-before-unregister *ordering* — Unregister never
// waits for requests in flight, so reversing the two still closes the
// channels before Disable() returns and this test still passes. It also
// cannot cover a stream whose client has stopped reading, which leaves the
// handler blocked in a write where a closed channel goes unobserved.
func TestDisableTerminatesLiveStream(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	served := make(chan struct{})
	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect/events", nil))
		close(served)
	}()

	for i := 0; i < 200; i++ {
		if s.subscribers() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s.subscribers() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", s.subscribers())
	}

	s.Disable()

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream outlived Disable()")
	}

	if s.subscribers() != 0 {
		t.Errorf("subscriber not cleaned up: %d remain", s.subscribers())
	}
}

func TestEventSubscriberLimit(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	// Fill the subscriber table directly; the HTTP path is covered above.
	var channels []chan uint64
	for i := 0; i < maxSubscribers; i++ {
		ch, ok := s.subscribe()
		if !ok {
			t.Fatalf("subscriber %d rejected below the limit", i)
		}
		channels = append(channels, ch)
	}

	if _, ok := s.subscribe(); ok {
		t.Errorf("subscriber %d accepted above the limit of %d",
			maxSubscribers+1, maxSubscribers)
	}

	s.unsubscribe(channels[0])

	if _, ok := s.subscribe(); !ok {
		t.Error("subscribing failed after a slot was freed")
	}
}

func TestEventsRejectedAboveLimit(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	for i := 0; i < maxSubscribers; i++ {
		if _, ok := s.subscribe(); !ok {
			t.Fatalf("subscriber %d rejected below the limit", i)
		}
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect/events", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, expected %d when full",
			w.Code, http.StatusServiceUnavailable)
	}
}
