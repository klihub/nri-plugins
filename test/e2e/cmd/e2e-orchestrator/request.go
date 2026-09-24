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
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// A field name is a shell-safe identifier, because every field becomes an
// environment variable and anything else could not be read back by a hook.
var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Names the orchestrator sets itself. A request carrying one is refused rather
// than quietly overwritten, so a producer is never left believing it said
// something it did not.
//
// attempt is deliberately not reserved: a requeued request carries it, and both
// acceptRequest and activeKeys must parse it. A producer who forges it only
// denies itself a retry.
var reservedKeys = []string{"job", "queued_at", "started_at"}

// Suffixes which say how a value is encoded. The encoding of a field is fixed by
// whoever defines the field, never chosen per request, so a consumer of a field
// handles exactly one form.
const (
	jsonSuffix = "_JSON"
	fileSuffix = "_FILE"
)

// Field is one line of a request.
type Field struct {
	Name  string
	Value string
}

// Request is what somebody wants done: an ordered set of fields.
//
// Ordered because a person reads these. The producer's order is what a queued
// request shows, which matters when the way to reproduce a problem is to copy one
// and change a line.
type Request struct {
	Fields []Field
}

// parseRequest reads a request and refuses anything it cannot mean.
func parseRequest(data []byte) (*Request, error) {
	req := &Request{}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		name, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%q is not a field", line)
		}
		if !fieldName.MatchString(name) {
			return nil, fmt.Errorf("%q is not a usable field name", name)
		}
		if slices.Contains(reservedKeys, name) {
			return nil, fmt.Errorf("%q is ours to set, not yours", name)
		}
		if strings.HasSuffix(name, jsonSuffix) && !json.Valid([]byte(value)) {
			return nil, fmt.Errorf("%s is not valid JSON", name)
		}

		req.Set(name, value)
	}

	if len(req.Fields) == 0 {
		return nil, fmt.Errorf("a request with no fields asks for nothing")
	}

	return req, nil
}

// Bytes is the request as it is stored.
func (r *Request) Bytes() []byte {
	var out strings.Builder

	for _, field := range r.Fields {
		out.WriteString(field.Name)
		out.WriteString("=")
		out.WriteString(field.Value)
		out.WriteString("\n")
	}

	return []byte(out.String())
}

// Get is the value of a field, and whether it was there at all. An empty value is
// a value: a hook may want to tell "set to nothing" from "not set".
func (r *Request) Get(name string) (string, bool) {
	for _, field := range r.Fields {
		if field.Name == name {
			return field.Value, true
		}
	}

	return "", false
}

// Set replaces a field in place, or appends it. In place so that rewriting a
// _FILE value leaves the request looking as the producer wrote it.
func (r *Request) Set(name, value string) {
	for i := range r.Fields {
		if r.Fields[i].Name == name {
			r.Fields[i].Value = value
			return
		}
	}

	r.Fields = append(r.Fields, Field{Name: name, Value: value})
}

// Key is the dedup key, or empty when the producer gave none.
//
// Opaque on purpose. The orchestrator compares it to decide whether work is
// already queued and reads nothing into what it says, so what counts as the same
// work is the producer's business.
func (r *Request) Key() string {
	key, _ := r.Get("key")

	return key
}

// fileFields are the fields naming a file, in order.
func (r *Request) fileFields() []Field {
	var found []Field

	for _, field := range r.Fields {
		if strings.HasSuffix(field.Name, fileSuffix) {
			found = append(found, field)
		}
	}

	return found
}
