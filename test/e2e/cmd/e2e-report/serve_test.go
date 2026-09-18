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

package main

import (
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newRoot publishes a run under a result root, packed or as it is.
func newRoot(t *testing.T, packed bool) (string, string) {
	t.Helper()

	root, name := t.TempDir(), "test-2026-09-16-0910"
	dir := filepath.Join(root, name)
	if err := os.Rename(newRun(t), dir); err != nil {
		t.Fatal(err)
	}
	if packed {
		if err := packRun(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(name, filepath.Join(root, "latest")); err != nil {
		t.Fatal(err)
	}

	return root, name
}

func get(t *testing.T, root, path string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, root, http.MethodGet, path)
}

func request(t *testing.T, root, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, root, false, method, path)
}

// getLive asks a server which builds the index of runs for every request.
func getLive(t *testing.T, root, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, root, true, http.MethodGet, path)
}

// liveServer is one server to ask more than once, for what it remembers between
// requests.
func liveServer(t *testing.T, root string) *Server {
	t.Helper()

	server, err := NewServer(root, true)
	if err != nil {
		t.Fatal(err)
	}

	return server
}

func ask(t *testing.T, server *Server, path string) string {
	t.Helper()

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s: %d, expected 200", path, recorder.Code)
	}

	return recorder.Body.String()
}

// rewriteResults puts data in the results.json of a run, leaving its size and
// its modification time as they were: a change nothing can notice by looking,
// which is how a test tells a cached answer from a fresh one.
func rewriteResults(t *testing.T, dir, data string) {
	t.Helper()

	path := filepath.Join(dir, resultsJSON)
	was, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if int(was.Size()) != len(data) {
		t.Fatalf("results.json is %d bytes, the replacement %d", was.Size(), len(data))
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, was.ModTime(), was.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func serve(t *testing.T, root string, live bool, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	server, err := NewServer(root, live)
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))

	return recorder
}

// TestServePacked checks that a packed run serves as if it had been extracted.
func TestServePacked(t *testing.T) {
	root, name := newRoot(t, true)

	for file, data := range files {
		got := get(t, root, "/"+name+"/"+file)
		if got.Code != http.StatusOK {
			t.Errorf("GET %s: %d, expected 200", file, got.Code)
			continue
		}
		if got.Body.String() != data {
			t.Errorf("GET %s: %q, expected %q", file, got.Body, data)
		}
	}

	// Through the link at the latest results as well.
	if got := get(t, root, "/latest/"+runnerLog); got.Body.String() != files[runnerLog] {
		t.Errorf("GET /latest/%s: %q", runnerLog, got.Body)
	}
}

// TestServeUnpacked checks that a run which was never packed still serves.
func TestServeUnpacked(t *testing.T) {
	root, name := newRoot(t, false)

	for file, data := range files {
		got := get(t, root, "/"+name+"/"+file)
		if got.Code != http.StatusOK || got.Body.String() != data {
			t.Errorf("GET %s: %d %q, expected 200 %q", file, got.Code, got.Body, data)
		}
	}
}

func TestServeContentType(t *testing.T) {
	root, name := newRoot(t, true)
	test := "/" + name + "/vm/" + suiteDir + "/balloons/test01/"

	for _, tc := range []struct{ path, kind string }{
		// The command transcripts of a test have no extension, and are text
		// worth showing in a browser instead of downloading.
		{test + "commands/0001-vm", "text/plain"},
		{test + testLog, "text/plain"},
		{"/" + name + "/" + indexHTML, "text/html"},
		{"/" + name + "/" + resultsJSON, "application/json"},
	} {
		got := get(t, root, tc.path).Header().Get("Content-Type")
		if !strings.HasPrefix(got, tc.kind) {
			t.Errorf("GET %s: content type %q, expected %q", tc.path, got, tc.kind)
		}
	}
}

func TestServeListing(t *testing.T) {
	root, name := newRoot(t, true)

	// The index of the run is served for the run itself.
	if got := get(t, root, "/"+name+"/"); got.Body.String() != files[indexHTML] {
		t.Errorf("GET /%s/: %q, expected the report", name, got.Body)
	}

	got := get(t, root, "/"+name+"/vm/"+suiteDir+"/balloons/test01/")
	if got.Code != http.StatusOK {
		t.Fatalf("listing a directory of the archive: %d", got.Code)
	}
	for _, expected := range []string{"commands/", testLog, summaryTxt} {
		if !strings.Contains(got.Body.String(), ">"+expected+"<") {
			t.Errorf("the listing does not name %s", expected)
		}
	}

	// A directory of the archive without the trailing slash, which relative
	// links in a listing need.
	got = get(t, root, "/"+name+"/vm")
	if got.Code != http.StatusMovedPermanently {
		t.Errorf("GET a directory without a trailing slash: %d, expected 301", got.Code)
	}
}

func TestServeNotFound(t *testing.T) {
	root, name := newRoot(t, true)

	for _, path := range []string{
		"/" + name + "/no/such/file",
		"/" + name + "/vm/" + suiteDir + "/balloons/test01/nosuchfile",
		"/no-such-run/index.html",
		"/" + name + "/vm/" + suiteDir + "/balloons/test01/commands/0001-vm/deeper",
	} {
		if got := get(t, root, path); got.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, expected 404", path, got.Code)
		}
	}
}

// TestServeStaysUnderRoot checks that nothing outside the published results is
// served, however a request asks for it.
func TestServeStaysUnderRoot(t *testing.T) {
	root, name := newRoot(t, true)

	secret := filepath.Join(filepath.Dir(root), "secret")
	if err := os.WriteFile(secret, []byte("not yours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A link out of the results, which is all it takes to serve anything.
	if err := os.Symlink(secret, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, name, "outside")); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/../secret",
		"/../../etc/passwd",
		"/" + name + "/../../secret",
		"/%2e%2e/secret",
		"/outside",
		"/" + name + "/outside",
	} {
		got := get(t, root, path)
		if got.Code == http.StatusOK && strings.Contains(got.Body.String(), "not yours") {
			t.Errorf("GET %s served what is outside the results", path)
		}
	}
}

func TestServeMethods(t *testing.T) {
	root, name := newRoot(t, true)

	got := request(t, root, http.MethodHead, "/"+name+"/"+runnerLog)
	if got.Code != http.StatusOK {
		t.Errorf("HEAD: %d, expected 200", got.Code)
	}
	if got.Body.Len() != 0 {
		t.Errorf("HEAD has a body: %q", got.Body)
	}
	if got.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD tells no content length")
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		got := request(t, root, method, "/"+name+"/"+runnerLog)
		if got.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, expected 405", method, got.Code)
		}
	}
}

// TestServeRevalidates checks that a browser cannot go on showing the report of
// a run which has been reported on again since.
func TestServeRevalidates(t *testing.T) {
	root, name := newRoot(t, false)
	page := "/" + name + "/" + indexHTML

	got := get(t, root, page)
	if cache := got.Header().Get("Cache-Control"); cache != "no-cache" {
		t.Errorf("Cache-Control of a report: %q, expected no-cache", cache)
	}
	stamp := got.Header().Get("Last-Modified")
	if stamp == "" {
		t.Fatalf("a report is served without Last-Modified")
	}

	// Asking about the copy one has is answered, and answered again once the
	// report has been written anew.
	server, err := NewServer(root, false)
	if err != nil {
		t.Fatal(err)
	}
	ask := func() int {
		request := httptest.NewRequest(http.MethodGet, page, nil)
		request.Header.Set("If-Modified-Since", stamp)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if code := ask(); code != http.StatusNotModified {
		t.Errorf("asking about an unchanged report: %d, expected 304", code)
	}

	local := filepath.Join(root, name, indexHTML)
	if err := os.WriteFile(local, []byte("<html>reported again</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(local, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if code := ask(); code != http.StatusOK {
		t.Errorf("asking about a report written anew: %d, expected 200", code)
	}
}

func TestServeNoSuchRoot(t *testing.T) {
	if _, err := NewServer(filepath.Join(t.TempDir(), "nowhere"), false); err == nil {
		t.Errorf("serving a directory which is not there did not fail")
	}
}

// TestServeLiveIndexCaches checks that the runs are not read again for a root
// which has not changed. The proof is a change no amount of looking can see:
// results.json rewritten to the same size, with its modification time put back.
func TestServeLiveIndexCaches(t *testing.T) {
	root, name := newRoot(t, false)
	server := liveServer(t, root)

	first := ask(t, server, "/")
	if strings.Contains(first, ">FAIL<") {
		t.Fatalf("the run reads FAIL before anything changed it")
	}

	rewriteResults(t, filepath.Join(root, name), `{"verdict":"FAIL" }`)

	if again := ask(t, server, "/"); again != first {
		t.Errorf("the runs were read again for a root which had not changed")
	}
}

// TestServeLiveIndexNoticesAChangedRun checks that a run reported on again is
// read again, which is what the cache must not get in the way of.
func TestServeLiveIndexNoticesAChangedRun(t *testing.T) {
	root, name := newRoot(t, false)
	server := liveServer(t, root)

	first := ask(t, server, "/")

	results := filepath.Join(root, name, resultsJSON)
	if err := os.WriteFile(results, []byte(`{"verdict":"FAIL"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(results, later, later); err != nil {
		t.Fatal(err)
	}

	again := ask(t, server, "/")
	if again == first {
		t.Errorf("a run reported on again was not read again")
	}
	if !strings.Contains(again, ">FAIL<") {
		t.Errorf("the verdict of the run did not change")
	}
}

// TestServeLiveIndexNoticesRunsComingAndGoing checks that a run published or
// pruned since the last request is listed, or stops being.
func TestServeLiveIndexNoticesRunsComingAndGoing(t *testing.T) {
	root, name := newRoot(t, false)
	server := liveServer(t, root)

	if body := ask(t, server, "/"); strings.Contains(body, "test-2026-09-18-0210") {
		t.Fatalf("a run which is not there yet is listed")
	}

	newOngoingRun(t, root, "test-2026-09-18-0210")
	if body := ask(t, server, "/"); !strings.Contains(body, "test-2026-09-18-0210") {
		t.Errorf("a run published since the last request is not listed")
	}

	if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if body := ask(t, server, "/"); strings.Contains(body, name) {
		t.Errorf("a run pruned since the last request is still listed")
	}
}

// TestServeLiveIndexNoticesARunGoingOn checks that a run still collecting is
// read again as it goes, where nothing but its log has changed.
func TestServeLiveIndexNoticesARunGoingOn(t *testing.T) {
	root, _ := newRoot(t, false)
	ongoing := newOngoingRun(t, root, "test-2026-09-18-0210")
	server := liveServer(t, root)

	first := ask(t, server, "/")

	// A test which has finished since, and the log the runner keeps appending.
	test := filepath.Join(ongoing, "vm", suiteDir, "balloons", "test01")
	if err := os.MkdirAll(test, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(test, summaryTxt),
		[]byte("Test verdict: PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(ongoing, runnerLog)
	if err := os.WriteFile(log, []byte("still going\nand going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(log, later, later); err != nil {
		t.Fatal(err)
	}

	if again := ask(t, server, "/"); again == first {
		t.Errorf("a run which collected another test since was not read again")
	}
}

// TestServeLiveIndexConcurrently checks that the index one server remembers
// survives being asked for from several requests at once, which is how it is
// asked for. Worth running under -race.
func TestServeLiveIndexConcurrently(t *testing.T) {
	root, name := newRoot(t, false)
	newOngoingRun(t, root, "test-2026-09-18-0210")
	server := liveServer(t, root)

	// One writer moving a run about under the readers, so that they race a
	// rebuild and not only each other.
	done := make(chan struct{})
	go func() {
		defer close(done)
		results := filepath.Join(root, name, resultsJSON)
		for i := range 20 {
			stamp := time.Now().Add(time.Duration(i) * time.Minute)
			_ = os.Chtimes(results, stamp, stamp)
		}
	}()

	var waiting sync.WaitGroup
	for range 8 {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			for range 20 {
				recorder := httptest.NewRecorder()
				server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
				if recorder.Code != http.StatusOK {
					t.Errorf("GET /: %d, expected 200", recorder.Code)
					return
				}
				if !strings.Contains(recorder.Body.String(), name) {
					t.Errorf("the index does not name %s", name)
					return
				}
			}
		}()
	}

	waiting.Wait()
	<-done
}

// TestServeLiveIndexSkipsLatest checks that the link at the newest run is not
// indexed as a run of its own, which would list the same run twice.
func TestServeLiveIndexSkipsLatest(t *testing.T) {
	root, name := newRoot(t, false)

	body := getLive(t, root, "/").Body.String()

	if strings.Contains(body, `href="latest/`) {
		t.Errorf("the link at the latest run is indexed as a run of its own")
	}
	if listed := strings.Count(body, `href="`+name+`/`); listed != 1 {
		t.Errorf("the run is listed %d times, expected once", listed)
	}
}

// snapshot records what is under root, so that a caller can tell whether
// anything about it changed.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	seen := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		seen[rel] = fmt.Sprintf("%d bytes, mode %s, %d", info.Size(), info.Mode(),
			info.ModTime().UnixNano())

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return seen
}

// TestServeLiveIndexWritesNothing checks that building the index reports on
// nothing. reportIndex writes a report for a run which has none, which is the
// tempting way to fill in a row; a server must not, and the one behind the
// systemd unit could not, as it is given the results read-only.
func TestServeLiveIndexWritesNothing(t *testing.T) {
	root, _ := newRoot(t, false)
	newOngoingRun(t, root, "test-2026-09-17-2251")

	before := snapshot(t, root)
	if got := getLive(t, root, "/"); got.Code != http.StatusOK {
		t.Fatalf("GET /: %d, expected 200", got.Code)
	}
	after := snapshot(t, root)

	if !maps.Equal(before, after) {
		for name, was := range before {
			if now, there := after[name]; !there {
				t.Errorf("%s went away, was %s", name, was)
			} else if now != was {
				t.Errorf("%s changed, was %s, is %s", name, was, now)
			}
		}
		for name := range after {
			if _, there := before[name]; !there {
				t.Errorf("%s was written", name)
			}
		}
	}
}

// TestServeLiveIndexPackedLikeUnpacked checks that a packed run is indexed
// exactly as an unpacked one, so that packing a run cannot be told from the
// index, let alone break a link in it.
func TestServeLiveIndexPackedLikeUnpacked(t *testing.T) {
	looseRoot, _ := newRoot(t, false)
	packedRoot, _ := newRoot(t, true)

	loose := getLive(t, looseRoot, "/").Body.String()
	packed := getLive(t, packedRoot, "/").Body.String()

	if loose != packed {
		t.Errorf("the index of a packed run differs from that of an unpacked one")
		for i := range min(len(loose), len(packed)) {
			if loose[i] != packed[i] {
				t.Errorf("first difference at %d:\n unpacked: %q\n   packed: %q",
					i, cut(loose, i), cut(packed, i))
				break
			}
		}
	}
}

// cut is the neighbourhood of i in s, for telling what differs where.
func cut(s string, i int) string {
	return s[max(0, i-40):min(len(s), i+40)]
}

// TestServeLiveIndexAtIndexHTML checks that the built index answers for the name
// of the file it stands in for, and not just for the root itself.
func TestServeLiveIndexAtIndexHTML(t *testing.T) {
	root, name := newRoot(t, false)
	if err := os.WriteFile(filepath.Join(root, indexHTML),
		[]byte("<html>stale</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/" + indexHTML} {
		got := getLive(t, root, path)
		if got.Code != http.StatusOK {
			t.Errorf("GET %s: %d, expected 200", path, got.Code)
			continue
		}
		if strings.Contains(got.Body.String(), "stale") {
			t.Errorf("GET %s served the index on disk", path)
		}
		if !strings.Contains(got.Body.String(), name) {
			t.Errorf("GET %s does not name the run %s", path, name)
		}
	}
}

// newOngoingRun adds a run which has no report yet, the way one looks while it
// is still going: the log of the runner and nothing to link to.
func newOngoingRun(t *testing.T, root, name string) string {
	t.Helper()

	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, runnerLog), []byte("still going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, statusTxt), []byte("RUNNING\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

// TestServeLiveIndexOngoingRun checks that a run with no report of its own is
// listed, and linked to by its directory, since there is no report to open.
func TestServeLiveIndexOngoingRun(t *testing.T) {
	root, _ := newRoot(t, false)
	ongoing := "test-2026-09-17-2251"
	newOngoingRun(t, root, ongoing)

	body := getLive(t, root, "/").Body.String()

	if !strings.Contains(body, `href="`+ongoing+`/"`) {
		t.Errorf("an ongoing run is not linked to by its directory: %q", body)
	}
	if strings.Contains(body, `href="`+ongoing+`/`+indexHTML+`"`) {
		t.Errorf("an ongoing run is linked to a report which is not there")
	}
}

// TestServeLiveIndex checks that the index of runs is built from the runs found
// under the root, and not read from the index.html lying next to them.
func TestServeLiveIndex(t *testing.T) {
	root, name := newRoot(t, false)
	stale := filepath.Join(root, indexHTML)
	if err := os.WriteFile(stale, []byte("<html>stale</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := getLive(t, root, "/")
	if got.Code != http.StatusOK {
		t.Fatalf("GET / with a live index: %d, expected 200", got.Code)
	}
	if strings.Contains(got.Body.String(), "stale") {
		t.Errorf("the index on disk was served instead of a built one")
	}
	if !strings.Contains(got.Body.String(), name) {
		t.Errorf("the built index does not name the run %s", name)
	}
}
