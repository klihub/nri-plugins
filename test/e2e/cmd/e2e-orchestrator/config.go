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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Where settings come from when nothing says otherwise. The file is optional and
// every setting in it has a default, so a host which wants the defaults needs no
// file. Overridable with E2E_ORCHESTRATOR_ENV so a test, or a second instance on
// one host, needs no root.
const defaultEnvFile = "/etc/sysconfig/nri-plugins-e2e-orchestrator"

// Config is what the orchestrator was told. Everything here is read at startup
// and never written, so a running job cannot be surprised by a change.
type Config struct {
	// Root holds queue/, jobs/, done/ and state/.
	Root string
	// Hooks is the root of the hook directories, one per event.
	Hooks string
	// MaxJobs is the cap: how many jobs may have a run hook going at once.
	MaxJobs int
	// HookTimeout bounds every hook but run, which is unbounded because work
	// taking hours is the normal case and a scheduler guessing is worse.
	HookTimeout time.Duration
	// DoneKeep is how many finished job directories to keep.
	DoneKeep int
}

func (c *Config) queueDir() string { return filepath.Join(c.Root, "queue") }
func (c *Config) jobsDir() string  { return filepath.Join(c.Root, "jobs") }
func (c *Config) doneDir() string  { return filepath.Join(c.Root, "done") }
func (c *Config) stateDir() string { return filepath.Join(c.Root, "state") }

// loadConfig reads the environment file, letting the real environment win.
//
// A setting already in the environment overrides the file, which is what lets a
// crontab line or a test override one thing without a file of its own. Same
// precedence as systemd's EnvironmentFile, so there is one rule to remember.
func loadConfig(envFile string) (*Config, error) {
	settings, err := readEnvFile(envFile)
	if err != nil {
		return nil, err
	}

	get := func(name, fallback string) string {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return value
		}
		if value, ok := settings[name]; ok && value != "" {
			return value
		}
		return fallback
	}

	cfg := &Config{Root: get("E2E_ROOT", "")}
	if cfg.Root == "" {
		return nil, fmt.Errorf("no E2E_ROOT, so there is nowhere to keep jobs")
	}
	if !filepath.IsAbs(cfg.Root) {
		return nil, fmt.Errorf("E2E_ROOT %q is not an absolute path", cfg.Root)
	}

	cfg.Hooks = get("E2E_HOOKS", filepath.Join(cfg.Root, "hooks.d"))

	if cfg.MaxJobs, err = positive(get("E2E_MAX_JOBS", "1"), "E2E_MAX_JOBS"); err != nil {
		return nil, err
	}
	if cfg.DoneKeep, err = positive(get("E2E_DONE_KEEP", "200"), "E2E_DONE_KEEP"); err != nil {
		return nil, err
	}

	seconds, err := positive(get("E2E_HOOK_TIMEOUT", "300"), "E2E_HOOK_TIMEOUT")
	if err != nil {
		return nil, err
	}
	cfg.HookTimeout = time.Duration(seconds) * time.Second

	return cfg, nil
}

// positive reads a setting which is meaningless at zero or below.
func positive(value, name string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: cannot make sense of %q", name, value)
	}
	if number < 1 {
		return 0, fmt.Errorf("%s: %d is not a usable value", name, number)
	}

	return number, nil
}

// readEnvFile reads KEY=value lines, ignoring blanks and comments. A missing file
// is not an error: every setting it could carry has a default.
func readEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	settings := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%s: %q is not a setting", path, line)
		}
		settings[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(value), `"`)
	}

	return settings, scanner.Err()
}
