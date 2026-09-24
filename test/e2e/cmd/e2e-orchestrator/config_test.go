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
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeEnv writes an environment file for loadConfig to read.
func writeEnv(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

// TestConfigDefaults checks that every setting but the root has a default, so an
// absent or nearly empty environment file is enough to run.
func TestConfigDefaults(t *testing.T) {
	root := t.TempDir()
	cfg, err := loadConfig(writeEnv(t, "E2E_ROOT="+root+"\n"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Hooks != filepath.Join(root, "hooks.d") {
		t.Errorf("hooks: got %q", cfg.Hooks)
	}
	if cfg.MaxJobs != 1 {
		t.Errorf("max jobs: got %d, want 1", cfg.MaxJobs)
	}
	if cfg.HookTimeout != 300*time.Second {
		t.Errorf("hook timeout: got %v, want 5m", cfg.HookTimeout)
	}
	if cfg.DoneKeep != 200 {
		t.Errorf("done keep: got %d, want 200", cfg.DoneKeep)
	}
	if cfg.queueDir() != filepath.Join(root, "queue") {
		t.Errorf("queue dir: got %q", cfg.queueDir())
	}
}

// TestConfigMissingFileIsNotAnError checks that the environment file is optional,
// since a host which wants every default should need no file at all. A root is
// still required, so this must fail for want of one and not for want of a file.
func TestConfigMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("E2E_ROOT", t.TempDir())

	if _, err := loadConfig(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("a missing environment file should be no error, got %v", err)
	}
}

// TestConfigRejectsNonsense checks that a setting which cannot mean what it says
// stops the run rather than silently becoming a default. A cap of zero would stop
// every job forever, which is worse than refusing to start.
func TestConfigRejectsNonsense(t *testing.T) {
	root := t.TempDir()
	for _, body := range []string{
		"E2E_ROOT=" + root + "\nE2E_MAX_JOBS=0\n",
		"E2E_ROOT=" + root + "\nE2E_MAX_JOBS=fish\n",
		"E2E_ROOT=" + root + "\nE2E_HOOK_TIMEOUT=-1\n",
		"E2E_ROOT=relative/path\n",
		"\n",
	} {
		if _, err := loadConfig(writeEnv(t, body)); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}

// TestConfigEmptyEnvironmentVariableIsNotAnOverride checks that an exported
// setting with nothing after the equals sign leaves the default alone. NAME= is
// the crontab idiom for "leave this be", and an empty override would otherwise
// point every hook directory at the filesystem root.
func TestConfigEmptyEnvironmentVariableIsNotAnOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("E2E_HOOKS", "")
	t.Setenv("E2E_MAX_JOBS", "")

	cfg, err := loadConfig(writeEnv(t, "E2E_ROOT="+root+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hooks != filepath.Join(root, "hooks.d") {
		t.Errorf("hooks: got %q, want the default", cfg.Hooks)
	}
	if cfg.MaxJobs != 1 {
		t.Errorf("max jobs: got %d, want 1", cfg.MaxJobs)
	}
}
