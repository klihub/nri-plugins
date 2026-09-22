// Copyright 2019-2020 Intel Corporation. All Rights Reserved.
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

package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStartStop(t *testing.T) {
	srv := NewServer()

	if err := srv.Start(":0"); err != nil {
		t.Errorf("failed to start HTTP server: %v", err)
	}

	srv.Stop()

	if err := srv.Start(":0"); err != nil {
		t.Errorf("failed to start HTTP server: %v", err)
	}

	if err := srv.Restart(":0"); err != nil {
		t.Errorf("failed to restart HTTP server on different port: %v", err)
	}

	if err := srv.Reconfigure(srv.GetAddress()); err != nil {
		t.Errorf("failed to reconfigure HTTP server on same port: %v", err)
	}
	if err := srv.Reconfigure(":0"); err != nil {
		t.Errorf("failed to reconfigure HTTP server on different port: %v", err)
	}

	srv.Stop()
}

func checkURL(t *testing.T, srv *Server, path, response string, status int) {
	url := "http://" + srv.GetAddress() + path

	res, err := http.Get(url)
	if err != nil {
		t.Errorf("http.Get(%s) failed: %v", url, err)
	}

	if res.StatusCode != status {
		t.Errorf("http.Get(%s) status %d, expected %d", url, res.StatusCode, status)
	}

	txt, err := io.ReadAll(res.Body)
	if err != nil {
		t.Errorf("http.Get(%s) failed to read response: %v", url, err)
	}

	if string(txt) != response {
		t.Errorf("http.Get(%s) unexpected response: %v, expected: %v", url, txt, response)
	}
}

type testHandler struct {
	response string
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", h.response) // nolint:errcheck
}

func TestPatternsp(t *testing.T) {
	srv := NewServer()
	mux := srv.GetMux()

	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Errorf("failed to start HTTP server: %v", err)
	}

	rh := &testHandler{"/"}
	ah := &testHandler{"a"}
	bh := &testHandler{"b"}
	ch := &testHandler{"c"}

	mux.Handle("/a", ah)
	checkURL(t, srv, "/a", "a", 200)

	mux.Handle("/b", bh)
	checkURL(t, srv, "/b", "b", 200)

	mux.Handle("/", rh)
	checkURL(t, srv, "/b", "b", 200)

	mux.Unregister("/b")
	checkURL(t, srv, "/b", "/", 200)

	mux.Handle("/b", ch)
	checkURL(t, srv, "/b", "c", 200)

	mux.Unregister("/a")
	checkURL(t, srv, "/a", "/", 200)

	srv.Stop()
}

func TestUnregisterWithRequestInFlight(t *testing.T) {
	// A handler must not hold up unregistration for as long as it runs. An
	// event stream runs for the lifetime of its connection, so a single
	// connected client would otherwise block every unregistration.
	mux := NewServeMux()

	entered := make(chan struct{})
	release := make(chan struct{})

	mux.HandleFunc("/blocking", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})

	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/blocking", nil))
	}()

	<-entered // the handler is running and will not return yet

	done := make(chan struct{})
	go func() {
		mux.Unregister("/blocking")
		close(done)
	}()

	select {
	case <-done:
		close(release)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Unregister() blocked on a request in flight")
	}
}

func TestStopWithRequestInFlight(t *testing.T) {
	// A handler must not hold up Stop() for as long as it runs. The
	// instrumentation server is restarted on every configuration update,
	// and that update holds locks a handler may need, so a long-lived
	// handler would otherwise wedge the update for its whole lifetime.
	srv := NewServer()
	mux := srv.GetMux()

	entered := make(chan struct{})
	release := make(chan struct{})

	mux.HandleFunc("/blocking", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})

	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("failed to start HTTP server: %v", err)
	}

	url := "http://" + srv.GetAddress() + "/blocking"

	go func() {
		res, err := http.Get(url)
		if err == nil {
			res.Body.Close() // nolint:errcheck
		}
	}()

	<-entered // the handler is running and will not return yet

	done := make(chan struct{})
	go func() {
		srv.Stop()
		close(done)
	}()

	select {
	case <-done:
		close(release)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Stop() blocked on a request in flight")
	}
}
