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
	"os"
)

// Every subcommand the orchestrator has. tick and request are what a crontab or a
// hook calls; emit is what a long-running hook calls; run-job is how tick
// supervises one job and is not meant to be typed.
const usage = `usage: e2e-orchestrator <command> [arguments]

commands:
  tick                    do what is due: run tick hooks, sweep, admit, start jobs
  request key=value ...   submit a request
  emit <event> [k=v ...]  dispatch hooks for an event in the calling job
  run-job <id>            supervise one job (started by tick, not by hand)

settings come from $E2E_ORCHESTRATOR_ENV, or ` + defaultEnvFile + `
`

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "e2e-orchestrator: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	command := os.Args[1]

	// Handle help without config.
	switch command {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "tick", "request", "emit", "run-job":
		// Valid commands, continue to load config.
	default:
		fatalf("no such command %q", command)
	}

	envFile := os.Getenv("E2E_ORCHESTRATOR_ENV")
	if envFile == "" {
		envFile = defaultEnvFile
	}

	cfg, err := loadConfig(envFile)
	if err != nil {
		fatalf("%v", err)
	}

	// Execute the command.
	switch command {
	case "tick":
		if err := tick(cfg); err != nil {
			fatalf("%v", err)
		}
	case "request":
		if err := requestCommand(cfg, os.Args[2:]); err != nil {
			fatalf("%v", err)
		}
	case "emit":
		if err := emitCommand(cfg, os.Args[2:]); err != nil {
			fatalf("%v", err)
		}
	case "run-job":
		if len(os.Args) != 3 {
			fatalf("run-job wants exactly one job id")
		}
		os.Exit(runJob(cfg, os.Args[2]))
	}
}

func tick(cfg *Config) error                       { return fmt.Errorf("not implemented") }
func emitCommand(cfg *Config, args []string) error { return fmt.Errorf("not implemented") }
func runJob(cfg *Config, id string) int            { return 1 }
