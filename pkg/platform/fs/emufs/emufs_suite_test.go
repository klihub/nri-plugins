package emufs_test

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/containers/nri-plugins/pkg/platform/fs/emufs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEmufs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EmuFS Suite")
}

var _ = Describe("EmuFS", func() {
	It("should be possible to create", func() {
		emuFS, err := emufs.EmuFS(&emufs.Dir{
			Dirs: map[string]*emufs.Dir{
				"a": {
					Files: map[string]emufs.File{
						"a-1": "a-one",
						"a-2": "a-two",
						"a-3": "aakol",
					},
					Links: map[string]emufs.Link{
						"b": "../e/f",
					},
				},
				"b": {
					Links: map[string]emufs.Link{
						"c": "../a/b",
					},
				},
				"e": {
					Links: map[string]emufs.Link{
						"f": "/aakol",
					},
				},
			},
			Files: map[string]emufs.File{
				"one":   "1",
				"two":   "2",
				"three": "3",
			},
			Links: map[string]emufs.Link{
				"yksi":  "one",
				"kaksi": "two",
				"kolme": "three",
				"aakol": "/a/a-3",
			},
		})

		Expect(err).To(BeNil())
		Expect(emuFS).ToNot(BeNil())

		err = fstest.TestFS(emuFS,
			"a/a-1",
			"a/a-2",
			"one",
			"two",
			"three",
			"yksi",
			"kaksi",
			"kolme",
			"b/c",
		)

		if err != nil {
			fmt.Printf("got errors: %s\n", err.Error())
		}

		Expect(err).To(BeNil())
	})
})
