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

package parser_test

import (
	"bytes"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/containers/nri-plugins/pkg/platform/parser"
)

func TestParser(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Parser Suite")
}

var _ = Describe("Parsing", func() {
	When("there is a content getter", func() {
		var (
			options = map[string][]parser.SimpleParserOption{
				"1234567": {
					parser.WithSourceReader(
						func(string) ([]byte, error) {
							return []byte("1234567"), nil
						},
					),
				},
				"FailRead": {
					parser.WithSourceReader(
						func(string) ([]byte, error) {
							return nil, fmt.Errorf("fail")
						},
					),
				},
			}
		)

		DescribeTable("should use the content getter to get the content",
			func(getter string, input string, expected string, fail bool) {
				var (
					result string
					err    error
					parse  = func(fields []string) error {
						result = fields[0]
						return nil
					}
				)

				p := parser.NewSimpleParser(parse, options[getter]...)

				err = p.Parse(input)
				if !fail {
					Expect(err).To(BeNil())
					Expect(result).Should(Equal(expected))
				} else {
					Expect(err).ToNot(BeNil())
				}
			},

			Entry("static 1234567", "1234567", "", "1234567", false),
			Entry("or fail accordingly", "FailRead", "1,2,3", nil, true),
		)

	})

	When("there is a preprocessor", func() {
		var (
			trimNuls = func(content []byte) ([]byte, error) {
				return bytes.TrimRight(content, "\x00"), nil
			}
			trimCR = func(content []byte) ([]byte, error) {
				return bytes.TrimRight(content, "\r"), nil
			}
			trimLF = func(content []byte) ([]byte, error) {
				return bytes.TrimRight(content, "\n"), nil
			}
			fail = func([]byte) ([]byte, error) {
				return nil, fmt.Errorf("fail")
			}

			options = map[string][]parser.SimpleParserOption{
				"TrimSpace":     {parser.WithPreprocessor(parser.TrimSpace)},
				"trimNuls":      {parser.WithPreprocessor(trimNuls)},
				"trimCR":        {parser.WithPreprocessor(trimCR)},
				"trimLF":        {parser.WithPreprocessor(trimLF)},
				"trimLFAndNuls": {parser.WithPreprocessors(trimNuls, trimLF)},
				"fail":          {parser.WithPreprocessor(fail)},
			}
		)

		DescribeTable("should preprocess input with the given preprocessor",
			func(preproc string, input string, expected string, fail bool) {
				var (
					result string
					err    error
					parse  = func(fields []string) error {
						result = fields[0]
						return nil
					}
				)

				p := parser.NewSimpleParser(
					parse,
					append(
						options[preproc],
						parser.WithEntrySplitter(parser.DontSplit),
						parser.WithFieldSplitter(parser.DontSplit),
					)...,
				)

				err = p.Parse(input)
				if !fail {
					Expect(err).To(BeNil())
					Expect(result).Should(Equal(expected))
				} else {
					Expect(err).ToNot(BeNil())
				}
			},

			Entry("TrimSpace", "TrimSpace", "test \r\r\n\t  ", "test", false),
			Entry("trimNuls", "trimNuls", "test\n\x00\x00\x00", "test\n", false),
			Entry("trimCR", "trimCR", "test\r\r\r", "test", false),
			Entry("trimLF", "trimLF", "test\n\n\n", "test", false),
			Entry("trimLFAndNuls", "trimLFAndNuls", "test\n\n\n\x00\x00\x00", "test", false),
			Entry("failure", "fail", "test", "", true),
		)
	})

	When("there is an entry splitter", func() {
		var (
			options = map[string][]parser.SimpleParserOption{
				"ByLines":  {parser.WithEntrySplitter(parser.SplitByLines)},
				"BySpaces": {parser.WithEntrySplitter(parser.SplitBySpaces)},
				"ByCommas": {parser.WithEntrySplitter(parser.SplitByCommas)},
				"Fail": {
					parser.WithEntrySplitter(
						func(string) ([]string, error) {
							return nil, fmt.Errorf("fail")
						},
					),
				},
			}
		)

		DescribeTable("should split entries using the given splitter",
			func(splitter string, input string, expected []string, fail bool) {
				var (
					result []string
					err    error
					parse  = func(fields []string) error {
						result = append(result, fields[0])
						return nil
					}
				)

				p := parser.NewSimpleParser(parse, options[splitter]...)

				err = p.Parse(input)
				if !fail {
					Expect(err).To(BeNil())
					Expect(result).Should(Equal(expected))
				} else {
					Expect(err).ToNot(BeNil())
				}
			},

			Entry("SplitByLines", "ByLines", "1\n2\n3", []string{"1", "2", "3"}, false),
			Entry("SplitBySpaces", "BySpaces", "1 2 3", []string{"1", "2", "3"}, false),
			Entry("SplitByCommas", "ByCommas", "1,2,3", []string{"1", "2", "3"}, false),
			Entry("or fail accordingly", "Fail", "1,2,3", nil, true),
		)
	})

	When("there is a field splitter", func() {
		var (
			options = map[string][]parser.SimpleParserOption{
				"ByLines":  {parser.WithFieldSplitter(parser.SplitByLines)},
				"BySpaces": {parser.WithFieldSplitter(parser.SplitBySpaces)},
				"ByCommas": {parser.WithFieldSplitter(parser.SplitByCommas)},
				"Fail": {
					parser.WithFieldSplitter(
						func(string) ([]string, error) {
							return nil, fmt.Errorf("fail")
						},
					),
				},
			}
		)

		DescribeTable("should split fields using the given splitter",
			func(splitter string, input string, expected []string, fail bool) {
				var (
					result []string
					err    error
					parse  = func(fields []string) error {
						result = append(result, fields...)
						return nil
					}
				)

				p := parser.NewSimpleParser(parse, options[splitter]...)

				err = p.Parse(input)
				if !fail {
					Expect(err).To(BeNil())
					Expect(result).Should(Equal(expected))
				} else {
					Expect(err).ToNot(BeNil())
				}
			},

			Entry("SplitByLines", "ByLines", "1\n2\n3", []string{"1", "2", "3"}, false),
			Entry("SplitBySpaces", "BySpaces", "1 2 3", []string{"1", "2", "3"}, false),
			Entry("SplitByCommas", "ByCommas", "1,2,3", []string{"1", "2", "3"}, false),
			Entry("or fail accordingly", "Fail", "1,2,3", nil, true),
		)
	})

	When("there are both entry and field splitters", func() {
		var (
			options = map[string][]parser.SimpleParserOption{
				"Lines,Spaces": {
					parser.WithEntrySplitter(parser.SplitByLines),
					parser.WithFieldSplitter(parser.SplitBySpaces),
				},
				"Lines,Commas": {
					parser.WithEntrySplitter(parser.SplitByLines),
					parser.WithFieldSplitter(parser.SplitByCommas),
				},
				"Spaces,Equal": {
					parser.WithEntrySplitter(parser.SplitBySpaces),
					parser.WithFieldSplitter(parser.SplitByEquals),
				},
				"FailEntry": {
					parser.WithEntrySplitter(
						func(string) ([]string, error) {
							return nil, fmt.Errorf("fail")
						},
					),
				},
				"FailField": {
					parser.WithFieldSplitter(
						func(string) ([]string, error) {
							return nil, fmt.Errorf("fail")
						},
					),
				},
			}
		)

		DescribeTable("should split entries first then fields using the splitters",
			func(splitters string, input string, expected []string, fail bool) {
				var (
					result []string
					err    error
					parse  = func(fields []string) error {
						result = append(result, fields...)
						return nil
					}
				)

				p := parser.NewSimpleParser(parse, options[splitters]...)

				err = p.Parse(input)
				if !fail {
					Expect(err).To(BeNil())
					Expect(result).Should(Equal(expected))
				} else {
					Expect(err).ToNot(BeNil())
				}
			},

			Entry("Lines,Spaces", "Lines,Spaces",
				"1 2\n3 4\n5 6", []string{"1", "2", "3", "4", "5", "6"}, false),
			Entry("Lines,Commas", "Lines,Commas",
				"1,2\n3,4\n5,6", []string{"1", "2", "3", "4", "5", "6"}, false),
			Entry("Spaces,Equal", "Spaces,Equal",
				"a=1 b=2 c=3 d=4", []string{"a", "1", "b", "2", "c", "3", "d", "4"}, false),
			Entry("or fail by entry", "FailEntry", "1,2,3", nil, true),
			Entry("or fail by field", "FailField", "1,2,3", nil, true),
		)
	})

})
