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

// These tests cover what the differential test cannot reach: the write paths,
// the pieces independent of a discovered machine, and FromMachine.

package system_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/lib/hardware/system"
	"github.com/containers/nri-plugins/pkg/sysfs" //nolint:staticcheck // deprecated on purpose: this is what it is compared against
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"
)

// TestEquivalentEPPParsing checks the EPP names round-trip the same way in both.
func TestEquivalentEPPParsing(t *testing.T) {
	for _, name := range []string{
		"performance", "balance_performance", "balance_power", "power",
		"", "nonsense",
	} {
		old := sysfs.EPPFromString(name)
		new := system.EPPFromString(name)
		if int(old) != int(new) {
			t.Errorf("EPPFromString(%q): pkg/sysfs %d, drop-in %d", name, old, new)
		}
		if old.String() != new.String() {
			t.Errorf("EPPFromString(%q).String(): pkg/sysfs %q, drop-in %q",
				name, old.String(), new.String())
		}
	}

	// every value's name round-trips
	for e := 0; e <= int(system.EPPUnknown); e++ {
		name := system.EPP(e).String()
		if name == "" {
			continue
		}
		if got := system.EPPFromString(name); int(got) != e {
			t.Errorf("EPP(%d).String() = %q parses back as %d", e, name, got)
		}
	}
}

// TestEquivalentFilterCombinators checks the And/Or/Not combinators.
func TestEquivalentFilterCombinators(t *testing.T) {
	p := discoverBoth(t, trees[0])
	ids := p.old.NodeIDs()

	cases := []struct {
		name string
		old  sysfs.NodeFilter
		new  system.NodeFilter
	}{
		{
			name: "And(HasMemory, HasLocalCPUs)",
			old:  sysfs.NodeFilterAnd(sysfs.NodeHasMemory, sysfs.NodeHasLocalCPUs),
			new:  system.NodeFilterAnd(system.NodeHasMemory, system.NodeHasLocalCPUs),
		},
		{
			name: "Or(PMEM, HBM)",
			old:  sysfs.NodeFilterOr(sysfs.NodeOfPMEMType, sysfs.NodeOfHBMType),
			new:  system.NodeFilterOr(system.NodeOfPMEMType, system.NodeOfHBMType),
		},
		{
			name: "Not(HasLocalCPUs)",
			old:  sysfs.NodeFilterNot(sysfs.NodeHasLocalCPUs),
			new:  system.NodeFilterNot(system.NodeHasLocalCPUs),
		},
		{
			name: "And()",
			old:  sysfs.NodeFilterAnd(),
			new:  system.NodeFilterAnd(),
		},
		{
			name: "Or()",
			old:  sysfs.NodeFilterOr(),
			new:  system.NodeFilterOr(),
		},
	}

	for _, tc := range cases {
		eqIDSet(t, "FilterNodes("+tc.name+")",
			p.old.FilterNodes(ids, tc.old), p.new.FilterNodes(ids, tc.new))
	}

	for _, ty := range []int{
		int(system.MemoryTypeDRAM), int(system.MemoryTypePMEM), int(system.MemoryTypeHBM),
	} {
		eqIDSet(t, "FilterNodes(NodeOfType)",
			p.old.FilterNodes(ids, sysfs.NodeOfType(sysfs.MemoryType(ty))),
			p.new.FilterNodes(ids, system.NodeOfType(system.MemoryType(ty))))
	}
}

// TestEquivalentUtilities checks the non-topology helpers the drop-in repeats.
func TestEquivalentUtilities(t *testing.T) {
	if got, want := system.GetMemoryCapacity(), sysfs.GetMemoryCapacity(); got != want {
		t.Errorf("GetMemoryCapacity: pkg/sysfs %d, drop-in %d", want, got)
	}

	cpus := mustParse(t, "0-3,8")
	eqIDSet(t, "IDSetFromCPUSet",
		sysfs.IDSetFromCPUSet(cpus), system.IDSetFromCPUSet(cpus))
	eqCPUSet(t, "CPUSetFromIDSet",
		sysfs.CPUSetFromIDSet(idset.NewIDSet(0, 1, 2, 3, 8)),
		system.CPUSetFromIDSet(idset.NewIDSet(0, 1, 2, 3, 8)))

	pick := func(line string) (string, string, error) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", "", nil
		}
		return fields[0], fields[1], nil
	}

	var oldTotal, newTotal uint64
	oerr := sysfs.ParseFileEntries("/proc/meminfo",
		map[string]any{"MemTotal:": &oldTotal}, pick)
	nerr := system.ParseFileEntries("/proc/meminfo",
		map[string]any{"MemTotal:": &newTotal}, pick)

	if (oerr == nil) != (nerr == nil) {
		t.Errorf("ParseFileEntries: errors differ: %v vs %v", oerr, nerr)
	}
	if oerr == nil && oldTotal != newTotal {
		t.Errorf("ParseFileEntries MemTotal: %d vs %d", oldTotal, newTotal)
	}

	oerr = sysfs.ParseFileEntries("/nonexistent", map[string]any{}, pick)
	nerr = system.ParseFileEntries("/nonexistent", map[string]any{}, pick)
	if (oerr == nil) != (nerr == nil) {
		t.Errorf("ParseFileEntries of a missing file: %v vs %v", oerr, nerr)
	}
}

// TestFromMachine checks that FromMachine agrees with DiscoverSystemAt.
func TestFromMachine(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", trees[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sys")); err != nil {
		t.Skipf("recorded tree %s is not unpacked: run ./test-setup.sh", trees[0])
	}

	m, err := hardware.Discover(hardware.WithRoot(root))
	if err != nil {
		t.Fatalf("hardware.Discover: %v", err)
	}

	wrapped := system.FromMachine(m)
	direct, err := system.DiscoverSystemAt(filepath.Join(root, "sys"))
	if err != nil {
		t.Fatalf("DiscoverSystemAt: %v", err)
	}

	eqCPUSet(t, "FromMachine CPUSet", direct.CPUSet(), wrapped.CPUSet())
	eqIDs(t, "FromMachine NodeIDs", direct.NodeIDs(), wrapped.NodeIDs())
	eqIDs(t, "FromMachine PackageIDs", direct.PackageIDs(), wrapped.PackageIDs())

	// Discover on an existing System is a no-op which reports success.
	if err := wrapped.Discover(system.DiscoverAll); err != nil {
		t.Errorf("Discover on an existing System: %v", err)
	}

	// SST is probed for a wrapped Machine too. Compare presence rather than
	// assert absence, so this holds with and without SST.
	if (wrapped.Sst() == nil) != (direct.Sst() == nil) {
		t.Errorf("SST platform presence differs: FromMachine %v, discovered %v",
			wrapped.Sst() != nil, direct.Sst() != nil)
	}
	cpu0 := m.CPUIDs()[0]
	if got, want := wrapped.CPU(cpu0).SstClos(), direct.CPU(cpu0).SstClos(); got != want {
		t.Errorf("SstClos = %d, want %d", got, want)
	}
	for _, id := range wrapped.PackageIDs() {
		w, d := wrapped.Package(id).SstInfo(), direct.Package(id).SstInfo()
		if (w == nil) != (d == nil) {
			t.Errorf("package#%d SST info presence differs: FromMachine %v, "+
				"discovered %v", id, w != nil, d != nil)
		}
	}
}

// TestWritePaths checks SetCpusOnline, SetCPUFrequencyLimits and
// CPU.SetFrequencyLimits on a writable copy of a recorded tree.
func TestWritePaths(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("testdata", trees[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "sys")); err != nil {
		t.Skipf("recorded tree %s is not unpacked: run ./test-setup.sh", trees[0])
	}

	root := t.TempDir()
	if out, err := runCp(src+"/sys", root); err != nil {
		t.Skipf("cannot copy the recorded tree: %v: %s", err, out)
	}

	sys, err := system.DiscoverSystemAt(filepath.Join(root, "sys"))
	if err != nil {
		t.Fatalf("DiscoverSystemAt: %v", err)
	}

	cpus := sys.CPUIDs()
	if len(cpus) < 2 {
		t.Skip("need at least two CPUs")
	}

	// a recorded tree lacks the attributes the writes touch
	cpuDir := filepath.Join(root, "sys", "devices", "system", "cpu",
		"cpu"+itoa(cpus[1]))
	freqDir := filepath.Join(cpuDir, "cpufreq")
	if err := os.MkdirAll(freqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		filepath.Join(cpuDir, "online"):            "1\n",
		filepath.Join(freqDir, "scaling_min_freq"): "0000000\n",
		filepath.Join(freqDir, "scaling_max_freq"): "0000000\n",
	} {
		if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("SetCpusOnline", func(t *testing.T) {
		changed, err := sys.SetCpusOnline(false, idset.NewIDSet(cpus[1]))
		if err != nil {
			t.Fatalf("SetCpusOnline: %v", err)
		}
		if !changed.Has(cpus[1]) {
			t.Errorf("cpu%d is not among the changed CPUs %v",
				cpus[1], changed.SortedMembers())
		}
		if got := readFile(t, filepath.Join(cpuDir, "online")); got != "0\n" {
			t.Errorf("online holds %q, want %q", got, "0\n")
		}

		changed, err = sys.SetCpusOnline(false, idset.NewIDSet(0))
		if err != nil {
			t.Fatalf("SetCpusOnline(cpu0): %v", err)
		}
		if changed.Has(0) {
			t.Error("cpu0 was taken offline")
		}
	})

	t.Run("SetFrequencyLimits", func(t *testing.T) {
		c := sys.CPU(cpus[1])
		if c.FrequencyRange().Min == 0 {
			t.Skip("the recorded tree has no cpufreq range to clamp against")
		}

		if err := c.SetFrequencyLimits(1_000_000, 9_000_000_000); err != nil {
			t.Fatalf("SetFrequencyLimits: %v", err)
		}

		freq := c.FrequencyRange()
		if got, want := readFile(t, filepath.Join(freqDir, "scaling_max_freq")),
			itoa(int(freq.Max))+"\n"; got != want {
			t.Errorf("scaling_max_freq holds %q, want %q (clamped)", got, want)
		}
	})

	t.Run("SetCPUFrequencyLimits", func(t *testing.T) {
		// CPUs without the attributes fail, so pass only the one
		// which has them
		err := sys.SetCPUFrequencyLimits(1_000_000, 2_000_000,
			idset.NewIDSet(cpus[1]))
		if err != nil {
			t.Fatalf("SetCPUFrequencyLimits: %v", err)
		}
	})

	// A System from FromMachine must write to the machine's own tree, not to
	// the real /sys.
	t.Run("FromMachine writes to the machine's tree", func(t *testing.T) {
		m, err := hardware.Discover(hardware.WithRoot(root))
		if err != nil {
			t.Fatalf("hardware.Discover: %v", err)
		}

		// The machine reads online state from the cpu-level "online" list,
		// which the earlier subtest did not touch. So it still sees cpu1 as
		// online and acts on taking it offline.
		online := filepath.Join(cpuDir, "online")
		if err := os.WriteFile(online, []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		changed, err := system.FromMachine(m).SetCpusOnline(false,
			idset.NewIDSet(cpus[1]))
		if err != nil {
			t.Fatalf("SetCpusOnline: %v", err)
		}
		if !changed.Has(cpus[1]) {
			t.Errorf("cpu%d is not among the changed CPUs %v",
				cpus[1], changed.SortedMembers())
		}
		if got := readFile(t, online); got != "0\n" {
			t.Errorf("online under the machine's root holds %q, want %q", got, "0\n")
		}
	})
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	blob, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(blob)
}

// runCp copies a directory tree with cp -a; Go has no library call for it.
func runCp(from, to string) (string, error) {
	out, err := exec.Command("cp", "-a", from, to).CombinedOutput()
	return string(out), err
}

// mustParse parses a cpuset or fails the test.
func mustParse(t *testing.T, s string) cpuset.CPUSet {
	t.Helper()
	cpus, err := cpuset.Parse(s)
	if err != nil {
		t.Fatalf("cpuset.Parse(%q): %v", s, err)
	}
	return cpus
}
