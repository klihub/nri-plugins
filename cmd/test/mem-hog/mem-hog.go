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
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	log *logrus.Logger
	hog = &Hog{}
)

const (
	PAGE_SIZE = 4096
)

type Hog struct {
	start  int
	final  int
	signal os.Signal
	touch  time.Duration
}

type ProcStatus struct {
	file   string
	status map[string]string
}

func parseMemAmount(s string) (int, error) {
	var (
		value = 0
		units = map[string]int{
			"": 1,

			"k": 1024,
			"M": 1024 * 1024,
			"G": 1024 * 1024 * 1024,
			"T": 1024 * 1024 * 1024 * 1024,

			"kB": 1024,

			"ki": 1000,
			"Mi": 1000 * 1000,
			"Gi": 1000 * 1000 * 1000,
			"Ti": 1000 * 1000 * 1000 * 1000,
		}
		unit string
	)

	for r := strings.NewReader(s); r.Len() > 0; {
		ch, _ := r.ReadByte()
		if '0' <= ch && ch <= '9' {
			value *= 10
			value += int(ch - '0')
			continue
		}

		if ch == ' ' || ch == '\t' {
			continue
		}

		unit = s[len(s)-r.Len()-1:]
		if len(s) == 1 {
			value = 1
		}

		break
	}

	u, ok := units[unit]
	if !ok {
		return 0, fmt.Errorf("invalid memory amount %q, invalid unit %q", s, unit)
	}

	return value * u, nil
}

func GetProcStatus(pid string) (*ProcStatus, error) {
	ps := &ProcStatus{
		file: "/proc/" + pid + "/status",
	}

	err := ps.Refresh()
	if err != nil {
		return nil, err
	}

	return ps, nil
}

func (ps *ProcStatus) Refresh() error {
	f, err := os.Open(ps.file)
	if err != nil {
		return fmt.Errorf("failed to open %s: %v", ps.file, err)
	}
	defer f.Close()

	buf, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read %s: %v", ps.file, err)
	}

	ps.status = make(map[string]string)

	for _, line := range strings.Split(string(buf), "\n") {
		hdrval := strings.SplitN(line, ":", 2)
		if len(hdrval) == 2 {
			hdr, val := strings.TrimSpace(hdrval[0]), strings.TrimSpace(hdrval[1])
			ps.status[hdr] = val
		}
	}

	return nil
}

func (ps *ProcStatus) Get(hdr string, refresh bool) (int, bool) {
	if refresh {
		if err := ps.Refresh(); err != nil {
			log.Errorf("failed to refresh %s: %v", ps.file, err)
		}
	}

	strv, ok := ps.status[hdr]
	if !ok {
		return 0, false
	}

	v, err := parseMemAmount(strv)
	if err != nil {
		log.Errorf("failed to parse status of %s (%s): %v", hdr, strv, err)
		return 0, false
	}

	return v, true
}

func (ps *ProcStatus) GetString(hdr string, refresh bool) (string, bool) {
	if refresh {
		if err := ps.Refresh(); err != nil {
			log.Errorf("failed to refresh %s: %v", ps.file, err)
		}
	}

	val, ok := ps.status[hdr]
	return val, ok
}

func (hog *Hog) Run() {
	var (
		sigCh = make(chan os.Signal)
		start = make([]byte, hog.start, hog.start)
		final []byte
		buf   = start
		tick  <-chan time.Time
	)

	ps, err := GetProcStatus("self")
	if err != nil {
		log.Fatalf("failed to get proc self status: %v", err)
	}

	signal.Notify(sigCh, hog.signal)

	if hog.touch != 0 {
		tick = time.NewTicker(hog.touch).C
	}

	hogMem := func(buf []byte) {
		var (
			kind = map[bool]string{false: "final", true: "initial"}[cap(buf) == hog.start]
		)

		log.Infof("hogging %s memory of %d bytes...", kind, cap(buf))

		for offs := 0; offs < cap(buf); offs += PAGE_SIZE {
			buf[offs] = 1
			if offs%(1024*1024) == 0 {
				log.Infof("hogged %d M of %s memory...", offs/(1024*1024), kind)
			}
		}

		ps.Refresh()
		rss, _ := ps.Get("VmRSS", true)
		log.Infof("done hogging, VmRSS now %v (%.2f M)", rss, float64(rss)/(1024.0*1024.0))
	}

	waitSignalOrTimer := func() {
		orTimer := map[bool]string{true: " or memory touch timer"}[tick != nil]
		log.Infof("waiting for hog signal %s%s, pid %d...",
			unix.SignalName(hog.signal.(unix.Signal)), orTimer, os.Getpid())

		select {
		case _ = <-sigCh:
			log.Info("received hog signal...")
			if final == nil {
				final = make([]byte, hog.final-hog.start, hog.final-hog.start)
			}
			buf = final
		case _ = <-tick:
			log.Info("memory touch timer expired...")
		}
	}

	for {
		hogMem(buf)
		waitSignalOrTimer()
	}
}

func main() {
	var (
		start  = flag.String("start", "100M", "memory allocation to start with")
		final  = flag.String("final", "1G", "memory allocation to hog to")
		hogSig = flag.String("signal", "SIGUSR1", "signal to start hogging memory")
		touch  = flag.Duration("touch-timer", 0, "touch all memory pages this often")
		err    error
	)

	log = logrus.StandardLogger()
	log.SetFormatter(&logrus.TextFormatter{PadLevelText: true})

	flag.Parse()

	hog.start, err = parseMemAmount(*start)
	if err != nil {
		log.Fatalf("failed to parse starting memory amount: %v", err)
	}

	hog.final, err = parseMemAmount(*final)
	if err != nil {
		log.Fatalf("failed to parse final memory amount: %v", err)
	}

	hog.signal = unix.SignalNum(*hogSig)
	if hog.signal == unix.Signal(0) {
		log.Fatalf("invalid signal name %q", *hogSig)
	}

	if hog.final < hog.start {
		log.Fatalf("invalid setup, final %d (%s) < start %d (%s)",
			hog.final, *final, hog.start, *start)
	}

	if *touch != 0 {
		hog.touch = *touch
	}

	hog.Run()
}
