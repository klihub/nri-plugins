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

// Package parser defines a basic interface for simple parsers.
// We define and use such parsers to extract information from the
// various kernel pseudofilesystems, such as sysfs and procfs.
package parser

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// A Parser parses a piece of data presented in some known format.
//
// The data can contain multiple entries. Each entry can consist of
// multiple fields. Parsing reads the data from a source which is
// typically a file or the readily acquired data content. The data
// is then optionally preprocessed and split into entries. Finally
// each entry is optinoally split into fields and fed to a parsing
// function. If any of the reading, preprocessing, or splitting
// phases fails with an error, parsing is aborted with that error.
type Parser interface {
	ReadSource(source string) ([]byte, error)
	Preprocess(content []byte) ([]byte, error)
	SplitEntries(data string) ([]string, error)
	SplitFields(entry string) ([]string, error)
	ParseFields(fields []string) error
}

// Parse the given source with the given parser.
func Parse(p Parser, source string) error {
	var (
		content []byte
		entries []string
		fields  []string
		err     error
	)

	content, err = p.ReadSource(source)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return fmt.Errorf("failed to read content: %w", err)
	}

	content, err = p.Preprocess(content)
	if err != nil {
		return fmt.Errorf("failed to preprocess content: %w", err)
	}

	entries, err = p.SplitEntries(string(content))
	if err != nil {
		return fmt.Errorf("failed split entries in content: %w", err)
	}

	for _, entry := range entries {
		fields, err = p.SplitFields(entry)
		if err != nil {
			return fmt.Errorf("failed to split fields in entry: %w", err)
		}

		err = p.ParseFields(fields)
		if err != nil {
			return fmt.Errorf("failed to parse fields: %w", err)
		}
	}

	return nil
}

// SimpleParser implements a (very) simple parser.
//
// By default a SimpleParser uses the source string as the content and
// feeds this content unpreprocessed to the parsing function as a single
// entry consisting of a single field. The defaults can be overridden
// using SimpleParserOptions.
type SimpleParser struct {
	read       func(source string) ([]byte, error)
	preprocess func(content []byte) ([]byte, error)
	entries    func(data string) ([]string, error)
	fields     func(entry string) ([]string, error)
	parse      func(fields []string) error
}

// SimpleParserOption is an option for a SimpleParser.
type SimpleParserOption func(*SimpleParser)

// WithSourceReader sets the content getter to the given file reader.
func WithSourceReader(readFn func(string) ([]byte, error)) SimpleParserOption {
	return func(p *SimpleParser) {
		p.read = readFn
	}
}

// WithSourceAsContent uses the content source string as the content itself.
// This option can also be omitted as it is the default behavior.
func WithSourceAsContent() SimpleParserOption {
	return func(p *SimpleParser) {
		p.read = func(source string) ([]byte, error) {
			return []byte(source), nil
		}
	}
}

// WithPreprocessor uses the single given preprocessor function.
func WithPreprocessor(preprocFn func([]byte) ([]byte, error)) SimpleParserOption {
	return func(p *SimpleParser) {
		p.preprocess = preprocFn
	}
}

// WithPreprocessors the given preprocessor functions, chaining them FIFO.
func WithPreprocessors(preprocFnSlice ...func([]byte) ([]byte, error)) SimpleParserOption {
	return func(p *SimpleParser) {
		p.preprocess = func(content []byte) ([]byte, error) {
			var err error
			for _, fn := range preprocFnSlice {
				content, err = fn(content)
				if err != nil {
					return nil, err
				}
			}
			return content, nil
		}
	}
}

// WithEntrySplitter uses the given entry splitter function.
func WithEntrySplitter(splitFn func(string) ([]string, error)) SimpleParserOption {
	return func(p *SimpleParser) {
		p.entries = splitFn
	}
}

// WithFieldSplitter uses the given field splitter function.
func WithFieldSplitter(splitFn func(string) ([]string, error)) SimpleParserOption {
	return func(p *SimpleParser) {
		p.fields = splitFn
	}
}

// NewSimpleParser creates a simple parser with the given parse function and options.
func NewSimpleParser(parse func([]string) error, options ...SimpleParserOption) *SimpleParser {
	p := &SimpleParser{
		parse: parse,
	}

	for _, opt := range options {
		opt(p)
	}

	return p
}

// ReadSource for the simple parser.
func (p *SimpleParser) ReadSource(source string) ([]byte, error) {
	var (
		content []byte
		err     error
	)

	if p.read != nil {
		content, err = p.read(source)
	} else {
		content = []byte(source)
	}

	return content, err
}

// Preprocess content for the simple parser.
func (p *SimpleParser) Preprocess(content []byte) ([]byte, error) {
	var err error

	if p.preprocess != nil {
		content, err = p.preprocess(content)
	}

	return content, err
}

// SplitEntries for the simple parser.
func (p *SimpleParser) SplitEntries(data string) ([]string, error) {
	var (
		entries []string
		err     error
	)

	if p.entries != nil {
		entries, err = p.entries(data)
	} else {
		entries = []string{data}
	}

	return entries, err
}

// SplitFields for the simple parser.
func (p *SimpleParser) SplitFields(entry string) ([]string, error) {
	var (
		fields []string
		err    error
	)

	if p.fields != nil {
		fields, err = p.fields(entry)
	} else {
		fields = []string{entry}
	}

	return fields, err
}

// ParseFields for the simple parser.
func (p *SimpleParser) ParseFields(fields []string) error {
	if p.parse == nil {
		return fmt.Errorf("invalid parser, nil parser function")
	}
	return p.parse(fields)
}

// Parse source with the simple parser.
func (p *SimpleParser) Parse(source string) error {
	return Parse(p, source)
}

// TrimSpace trims leading and trailing whitespace from the content.
func TrimSpace(content []byte) ([]byte, error) {
	return bytes.TrimSpace(content), nil
}

// TrimTrailingSpace trims trailing whitespace from the content.
func TrimTrailingSpace(content []byte) ([]byte, error) {
	return bytes.TrimRight(content, "\t \r\n"), nil
}

// TrimLaadingSpace trims trailing whitespace from the content.
func TrimLeadingSpace(content []byte) ([]byte, error) {
	return bytes.TrimLeft(content, "\t \r\n"), nil
}

// SplitByLines splits entries or fields by newline.
func SplitByLines(data string) ([]string, error) {
	return strings.Split(data, "\n"), nil
}

// SplitBySpaces splits entries or fields by space.
func SplitBySpaces(data string) ([]string, error) {
	return strings.Fields(data), nil
}

// SplitByCommas splits entries or fields by comma.
func SplitByCommas(data string) ([]string, error) {
	return strings.Split(data, ","), nil
}

// SplitByEquals splits entries or fields by an equal sign.
func SplitByEquals(data string) ([]string, error) {
	return strings.SplitN(data, "=", 2), nil
}

// SplitByColons splits entries or fields by a colon.
func SplitByColons(data string) ([]string, error) {
	return strings.SplitN(data, ":", 2), nil
}

// DontTrim does not trim content.
func DontTrim(content []byte) ([]byte, error) {
	return content, nil
}

// DontSplit does not split entries or fields.
func DontSplit(data string) ([]string, error) {
	return []string{data}, nil
}
