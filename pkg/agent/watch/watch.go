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

package watch

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/watch"

	logger "github.com/containers/nri-plugins/pkg/log"
)

type (
	EventType = watch.EventType
	Event     = watch.Event
	Interface = watch.Interface
)

const (
	Added    = watch.Added
	Modified = watch.Modified
	Deleted  = watch.Deleted
	Bookmark = watch.Bookmark
	Error    = watch.Error

	reopenDelay = 5 * time.Second
)

// CreateFn creates a watch for the named object.
type CreateFn func(ctx context.Context, ns, name string) (Interface, error)

// Watch is a wrapper for apimachinery watches that monitor a single object.
// The wrapper transparently reopens the watch when it expires.
type Watch struct {
	create    CreateFn
	ctx       context.Context
	namespace string
	name      string
	resultC   chan Event
	wif       watch.Interface
	pending   *Event
	reopenC   <-chan time.Time
	failing   bool

	stopLock sync.Mutex
	stopC    chan struct{}
	doneC    chan struct{}
}

var (
	log = logger.Get("agent")
)

// New sets up a watch for the named object using the watch creation function.
func New(ctx context.Context, ns, name string, create CreateFn) (Interface, error) {
	w := &Watch{
		create:    create,
		ctx:       ctx,
		namespace: ns,
		name:      name,
		resultC:   make(chan Event, watch.DefaultChanSize),
		stopC:     make(chan struct{}),
		doneC:     make(chan struct{}),
	}

	if err := w.run(); err != nil {
		return nil, err
	}

	return w, nil
}

// Stop stops the watch.
func (w *Watch) Stop() {
	w.stopLock.Lock()
	defer w.stopLock.Unlock()

	if w.stopC != nil {
		close(w.stopC)
		_ = <-w.doneC
		w.stopC = nil
	}
}

// ResultChan returns the watch channel for receiving events.
func (w *Watch) ResultChan() <-chan Event {
	return w.resultC
}

func (w *Watch) eventChan() <-chan Event {
	if w.wif == nil {
		return nil
	}
	return w.wif.ResultChan()
}

func (w *Watch) run() error {
	err := w.open()
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-w.stopC:
				w.stop()
				close(w.resultC)
				close(w.doneC)
				return

			case e, ok := <-w.eventChan():
				if !ok {
					log.Debug("watch %s expired", w.watchname())
					w.reopen()
					continue
				}

				if e.Type == Error {
					w.markFailing()
					w.stop()
					w.scheduleReopen()
				}

				select {
				case w.resultC <- e:
				default:
					w.markFailing()
					w.stop()
					w.scheduleReopen()
				}

			case <-w.reopenC:
				w.reopenC = nil
				w.reopen()
			}
		}
	}()

	return nil
}

func (w *Watch) open() error {
	if w.wif != nil {
		w.wif.Stop()
		w.wif = nil
	}

	wif, err := w.create(w.ctx, w.namespace, w.name)
	if err != nil {
		return err
	}

	w.wif = wif
	return nil
}

func (w *Watch) reopen() {
	if err := w.open(); err != nil {
		w.scheduleReopen()
	} else {
		log.Debug("watch %s reopened", w.watchname())
		w.markRunning()
	}
}

func (w *Watch) stop() {
	if w.wif != nil {
		w.wif.Stop()
		w.wif = nil
	}
}

func (w *Watch) scheduleReopen() {
	if w.reopenC != nil {
		return
	}
	w.reopenC = time.After(reopenDelay)
}

func (w *Watch) watchname() string {
	if w.namespace != "" {
		return w.namespace + "/" + w.name
	}
	return w.name
}

func (w *Watch) markFailing() {
	if !w.failing {
		log.Error("watch %s is now failing", w.watchname())
		w.failing = true
	}
}

func (w *Watch) markRunning() {
	if w.failing {
		log.Info("watch %s is now running again", w.watchname())
		w.failing = false
	}
}
