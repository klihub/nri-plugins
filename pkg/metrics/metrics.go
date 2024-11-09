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

package metrics

import (
	"path"
	"strings"
	"sync"
	"time"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	model "github.com/prometheus/client_model/go"
)

var (
	log = logger.Get("metrics")
)

type (
	State int

	Collector struct {
		collector prometheus.Collector
		name      string
		group     string
		State
		lastpoll []prometheus.Metric
	}

	CollectorOption func(*Collector)
)

const (
	Enabled State = (1 << iota)
	Polled
	NamespacePrefix
	SubsystemPrefix

	DefaultName = "default"
)

func WithoutNamespace() CollectorOption {
	return func(c *Collector) {
		c.State &^= NamespacePrefix
	}
}

func WithoutSubsystem() CollectorOption {
	return func(c *Collector) {
		c.State &^= SubsystemPrefix
	}
}

func WithPolled() CollectorOption {
	return func(c *Collector) {
		c.State |= Polled
	}
}

func (s State) IsEnabled() bool {
	return s&Enabled != 0
}

func (s State) IsPolled() bool {
	return s&Polled != 0
}

func (s State) NeedsNamespace() bool {
	return s&NamespacePrefix != 0
}

func (s State) NeedsSubsystem() bool {
	return s&SubsystemPrefix != 0
}

func (s State) String() string {
	var (
		str = ""
		sep = ""
	)

	if !(s.IsEnabled() || s.IsPolled()) {
		str = "disabled"
		sep = ","
	} else {
		if s.IsEnabled() {
			str += "enabled"
			sep = ","
		}
		if s.IsPolled() {
			str += sep + "polled"
			sep = ","
		}
	}
	if s.NeedsNamespace() {
		str += sep + "namespace-prefixed"
		sep = ","
	}
	if s.NeedsSubsystem() {
		str += sep + "subsystem-prefixed"
	}

	return str
}

func NewCollector(name string, collector prometheus.Collector, options ...CollectorOption) *Collector {
	c := &Collector{
		name:      name,
		collector: collector,
		State:     Enabled | NamespacePrefix | SubsystemPrefix,
	}

	for _, o := range options {
		o(c)
	}

	return c
}

func (c *Collector) Name() string {
	return c.group + "/" + c.name
}

func (c *Collector) Matches(glob string) bool {
	if glob == c.group || glob == c.name {
		return true
	}

	ok, err := path.Match(glob, c.group)
	if ok {
		return true
	}

	ok, err = path.Match(glob, c.name)
	if ok {
		return true
	}

	ok, err = path.Match(glob, c.Name())
	if ok {
		return true
	}

	if err != nil {
		log.Error("invalid glob pattern %q (name %s): %v", glob, c.Name(), err)
	}

	return false
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.collector.Describe(ch)
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	if !c.IsEnabled() {
		return
	}

	if !c.IsPolled() {
		c.collector.Collect(ch)
		return
	}

	for _, m := range c.lastpoll {
		ch <- m
	}
}

func (c *Collector) Poll() {
	if !c.IsPolled() || !c.IsEnabled() {
		return
	}

	log.Debug("polling collector %q", c.name)

	ch := make(chan prometheus.Metric, 32)
	go func() {
		c.collector.Collect(ch)
		close(ch)
	}()

	polled := make([]prometheus.Metric, 0, 16)
	for m := range ch {
		polled = append(polled, m)
	}

	c.lastpoll = polled[:]
}

func (c *Collector) Enable(state bool) {
	if state {
		c.State |= Enabled
	} else {
		c.State &^= Enabled
	}
}

func (c *Collector) Polled(state bool) {
	if state {
		c.State |= Polled
		c.State |= Enabled
	} else {
		c.State &^= Polled
	}
}

func (c *Collector) state() State {
	return c.State
}

type (
	Group struct {
		name       string
		collectors []*Collector
	}
)

func newGroup(name string) *Group {
	return &Group{name: name}
}

func (g *Group) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range g.collectors {
		c.Describe(ch)
	}
}

func (g *Group) Collect(ch chan<- prometheus.Metric) {
	for _, c := range g.collectors {
		c.Collect(ch)
	}
}

func (g *Group) Poll() {
	if !g.State().IsPolled() {
		return
	}

	log.Debug("polling group %s", g.name)

	wg := sync.WaitGroup{}
	for _, c := range g.collectors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Poll()
		}()
	}
	wg.Wait()
}

func (g *Group) State() State {
	var state State
	for _, c := range g.collectors {
		state |= c.state()
	}
	return state
}

func (g *Group) add(c *Collector) {
	c.group = g.name
	g.collectors = append(g.collectors, c)
	log.Info("registered collector %q", c.Name())
}

func (g *Group) Register(plain, ns prometheus.Registerer) error {
	var (
		plainGrp = prefixedRegisterer(g.name, plain)
		nsGrp    = prefixedRegisterer(g.name, ns)
	)

	for _, c := range g.collectors {
		var reg prometheus.Registerer

		if c.NeedsNamespace() {
			if c.NeedsSubsystem() {
				reg = nsGrp
			} else {
				reg = ns
			}
		} else {
			if c.NeedsSubsystem() {
				reg = plainGrp
			} else {
				reg = plain
			}
		}

		if err := reg.Register(c); err != nil {
			return err
		}
	}

	return nil
}

func (g *Group) Configure(enabled, polled []string) State {
	for _, c := range g.collectors {
		c.Enable(false)
		c.Polled(false)
	}

	state := State(0)
	for _, c := range g.collectors {
		for _, glob := range enabled {
			if c.Matches(glob) {
				c.Enable(true)
				log.Debug("collector %q now %s", c.Name(), c.state())
			}
			state |= c.state()
		}
		for _, glob := range polled {
			if c.Matches(glob) {
				c.Enable(true)
				c.Polled(true)
				log.Debug("collector %q now %s", c.Name(), c.state())
			}
			state |= c.state()
		}
	}

	log.Debug("group %q now %s", g.name, state)

	return state
}

type (
	Registry struct {
		groups map[string]*Group
		state  State
	}

	RegisterOptions struct {
		group string
		copts []CollectorOption
	}

	RegisterOption func(*RegisterOptions)
)

func WithGroup(name string) RegisterOption {
	return func(o *RegisterOptions) {
		if name == "" {
			name = DefaultName
		}
		o.group = name
	}
}

func WithCollectorOptions(opts ...CollectorOption) RegisterOption {
	return func(o *RegisterOptions) {
		o.copts = append(o.copts, opts...)
	}
}

func NewRegistry() *Registry {
	return &Registry{
		groups: make(map[string]*Group),
	}
}

func (r *Registry) Register(name string, collector prometheus.Collector, opts ...RegisterOption) error {
	options := &RegisterOptions{group: DefaultName}
	for _, o := range opts {
		o(options)
	}

	grp, ok := r.groups[options.group]
	if !ok {
		grp = newGroup(options.group)
		r.groups[grp.name] = grp
	}

	grp.add(NewCollector(name, collector, options.copts...))
	r.state = 0

	return nil
}

func (r *Registry) Configure(enabled []string, polled []string) State {
	log.Info("configuring registry with collectors enabled=[%s], polled=[%s]",
		strings.Join(enabled, ","), strings.Join(polled, ","))

	r.state = 0
	for _, g := range r.groups {
		r.state |= g.Configure(enabled, polled)
	}

	return r.state
}

func (r *Registry) Poll() {
	wg := sync.WaitGroup{}
	for _, g := range r.groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Poll()
		}()
	}
	wg.Wait()
}

func (r *Registry) State() State {
	if r.state == 0 {
		for _, g := range r.groups {
			r.state |= g.State()
		}
	}
	return r.state
}

func (r *Registry) Gatherer(opts ...GathererOption) (*Gatherer, error) {
	return r.NewGatherer(opts...)
}

func prefixedRegisterer(prefix string, reg prometheus.Registerer) prometheus.Registerer {
	if prefix != "" {
		return prometheus.WrapRegistererWithPrefix(prefix+"_", reg)
	}
	return reg
}

type (
	Gatherer struct {
		*prometheus.Registry
		r            *Registry
		namespace    string
		ticker       *time.Ticker
		pollInterval time.Duration
		stopCh       chan chan struct{}
		lock         sync.Mutex
		enabled      []string
		polled       []string
	}

	GathererOption func(*Gatherer)
)

const (
	MinPollInterval     = 5 * time.Second
	DefaultPollInterval = 30 * time.Second
)

func WithNamespace(namespace string) GathererOption {
	return func(g *Gatherer) {
		g.namespace = namespace
	}
}

func WithPollInterval(interval time.Duration) GathererOption {
	return func(g *Gatherer) {
		if interval < MinPollInterval {
			g.pollInterval = MinPollInterval
		} else {
			g.pollInterval = interval
		}
	}
}

func WithMetrics(enabled, polled []string) GathererOption {
	return func(g *Gatherer) {
		g.enabled = enabled
		g.polled = polled
	}
}

func (r *Registry) NewGatherer(opts ...GathererOption) (*Gatherer, error) {
	g := &Gatherer{
		r:            r,
		Registry:     prometheus.NewPedanticRegistry(),
		pollInterval: DefaultPollInterval,
	}

	for _, o := range opts {
		o(g)
	}

	r.Configure(g.enabled, g.polled)

	nsg := prefixedRegisterer(g.namespace, g.Registry)

	for _, grp := range r.groups {
		if err := grp.Register(g.Registry, nsg); err != nil {
			return nil, err
		}
	}

	g.start()

	return g, nil
}

func (g *Gatherer) Gather() ([]*model.MetricFamily, error) {
	g.Block()
	defer g.Unblock()

	mfs, err := g.Registry.Gather()
	if err != nil {
		return nil, err
	}

	return mfs, nil
}

func (g *Gatherer) Block() {
	g.lock.Lock()
}

func (g *Gatherer) Unblock() {
	g.lock.Unlock()
}

func (g *Gatherer) start() {
	g.Block()
	defer g.Unblock()

	if !g.r.State().IsPolled() {
		log.Info("no polling (no collectors in polled mode)")
		return
	}

	log.Info("will do periodic polling (some collectors in polled mode)")

	g.stopCh = make(chan chan struct{})
	g.ticker = time.NewTicker(g.pollInterval)

	g.r.Poll()
	go g.poller()
}

func (g *Gatherer) poller() {
	for {
		select {
		case doneCh := <-g.stopCh:
			g.ticker.Stop()
			g.ticker = nil
			close(doneCh)
			return
		case _ = <-g.ticker.C:
			g.Block()
			g.r.Poll()
			g.Unblock()
		}
	}
}

func (g *Gatherer) Stop() {
	g.Block()
	defer g.Unblock()

	if g.stopCh == nil {
		return
	}

	doneCh := make(chan struct{})
	g.stopCh <- doneCh
	_ = <-doneCh

	g.stopCh = nil
}

var (
	defaultRegistry *Registry
)

func Default() *Registry {
	if defaultRegistry == nil {
		defaultRegistry = NewRegistry()
	}
	return defaultRegistry
}

func Register(name string, collector prometheus.Collector, opts ...RegisterOption) error {
	return Default().Register(name, collector, opts...)
}

func NewGatherer(opts ...GathererOption) (*Gatherer, error) {
	return Default().Gatherer(opts...)
}
