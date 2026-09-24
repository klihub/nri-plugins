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
	"strings"
	"testing"
)

// TestRequestRoundTrip checks that a request survives being written and read, in
// the order it was written. Order is kept so that a person reading a queued
// request sees what the producer wrote rather than something sorted.
func TestRequestRoundTrip(t *testing.T) {
	body := "remote=https://example.com/r\nbranch=main\ntrigger=nightly\n"

	req, err := parseRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(req.Bytes()); got != body {
		t.Errorf("round trip: got %q, want %q", got, body)
	}
	if value, ok := req.Get("branch"); !ok || value != "main" {
		t.Errorf("branch: got %q, %v", value, ok)
	}
	if _, ok := req.Get("absent"); ok {
		t.Error("found a field which is not there")
	}
}

// TestRequestIgnoresBlanksAndComments checks that a request may be commented, so
// one written by hand to reproduce something can say why it exists.
func TestRequestIgnoresBlanksAndComments(t *testing.T) {
	req, err := parseRequest([]byte("# why\n\nbranch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Fields) != 1 {
		t.Fatalf("got %d fields, want 1", len(req.Fields))
	}
}

// TestRequestRejections checks every way a request is refused. A request is the
// one thing a stranger hands us, so each refusal is a case with a test rather
// than a default which quietly does something else.
func TestRequestRejections(t *testing.T) {
	for what, body := range map[string]string{
		"no equals sign":       "branch main\n",
		"empty name":           "=main\n",
		"name with a dash":     "my-field=x\n",
		"name starting digit":  "1field=x\n",
		"reserved key":         "job=x\n",
		"reserved key attempt": "attempt=2\n",
		"malformed json":       "candidates_JSON={not json\n",
		"nothing at all":       "\n",
	} {
		if _, err := parseRequest([]byte(body)); err == nil {
			t.Errorf("accepted %s: %q", what, body)
		}
	}
}

// TestRequestAcceptsValidJSON checks that a well formed _JSON field is kept
// verbatim, because the orchestrator validates it and never interprets it.
func TestRequestAcceptsValidJSON(t *testing.T) {
	req, err := parseRequest([]byte(`prs_JSON=[{"n":1},{"n":2}]` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := req.Get("prs_JSON")
	if value != `[{"n":1},{"n":2}]` {
		t.Errorf("got %q", value)
	}
}

// TestRequestValueIsOneLine checks that a value spanning lines is refused. It has
// to be: the format has no escaping, so a newline in a value would read back as
// two fields and the second would be nonsense.
func TestRequestValueIsOneLine(t *testing.T) {
	if _, err := parseRequest([]byte("tests=a\nb=c\n")); err != nil {
		t.Fatal("two plain lines are two fields, not an error")
	}

	req := &Request{}
	req.Set("tests", "a\nb")
	if _, err := parseRequest(req.Bytes()); err == nil {
		t.Error("a value with a newline in it round tripped")
	}
}

// TestRequestKey checks the dedup key, which is opaque: the orchestrator compares
// it and never reads anything into it.
func TestRequestKey(t *testing.T) {
	req, err := parseRequest([]byte("key=origin@main\nbranch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if req.Key() != "origin@main" {
		t.Errorf("key: got %q", req.Key())
	}

	none, err := parseRequest([]byte("branch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if none.Key() != "" {
		t.Errorf("a request with no key has one: %q", none.Key())
	}
}

// TestRequestFileFields checks that _FILE fields are found by suffix, since they
// are the ones whose targets have to be copied into the job.
func TestRequestFileFields(t *testing.T) {
	req, err := parseRequest([]byte("a=1\nb_FILE=/tmp/x\nc_JSON=[]\nd_FILE=/tmp/y\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := req.fileFields()
	if len(got) != 2 || got[0].Name != "b_FILE" || got[1].Name != "d_FILE" {
		t.Errorf("file fields: got %+v", got)
	}
}

// TestRequestSetReplaces checks that Set replaces rather than appends, so that
// rewriting a _FILE value to point into the job leaves one field and not two.
func TestRequestSetReplaces(t *testing.T) {
	req, err := parseRequest([]byte("a=1\nb=2\n"))
	if err != nil {
		t.Fatal(err)
	}
	req.Set("a", "3")
	if len(req.Fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(req.Fields))
	}
	if value, _ := req.Get("a"); value != "3" {
		t.Errorf("a: got %q, want 3", value)
	}
	if !strings.HasPrefix(string(req.Bytes()), "a=3\n") {
		t.Errorf("Set moved the field: %q", req.Bytes())
	}
}
