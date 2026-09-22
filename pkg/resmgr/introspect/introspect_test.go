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
	"net/http/httptest"
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

func TestDisableDoesNotDeadlockWithRequestInFlight(t *testing.T) {
	// Unregistering takes the mux' write lock, which waits for the requests
	// in flight, and a request in flight needs the server's mutex to store
	// its result. If Disable() held that mutex across the unregistration,
	// neither could finish.
	entered := make(chan struct{})
	release := make(chan struct{})
	served := make(chan struct{})
	disabled := make(chan struct{})

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
		close(served)
	}()

	<-entered // a request holds the mux read lock and is inside collect()

	go func() {
		s.Disable()
		close(disabled)
	}()

	time.Sleep(100 * time.Millisecond) // let Disable() reach the mux

	close(release)

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("request in flight never finished during Disable()")
	}
	select {
	case <-disabled:
	case <-time.After(2 * time.Second):
		t.Fatal("Disable() blocked on a request in flight")
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
