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

package topologyaware

import (
	"fmt"
	"math"
	"strconv"
	"time"

	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy/topologyaware"
	"github.com/containers/nri-plugins/pkg/kubernetes"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	libmem "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
	corev1 "k8s.io/api/core/v1"
)

const (
	AnnotationDomain        = "." + kubernetes.ResmgrKeyNamespace
	PreferReservedCpusKey   = "prefer-reserved-cpus" + AnnotationDomain
	PreferSharedCpusKey     = "prefer-shared-cpus" + AnnotationDomain
	PreferIsolatedCpusKey   = "prefer-isolated-cpus" + AnnotationDomain
	RequireIsolatedCpusKey  = "require-isolated-cpus" + AnnotationDomain
	SchedulingClassKey      = "scheduling-class" + AnnotationDomain
	CpuClassKey             = "cpu-class" + AnnotationDomain
	PreferCpuPriorityKey    = "prefer-cpu-priority" + AnnotationDomain
	IrqAffinityKey          = "irq-affinity" + AnnotationDomain
	HideHyperthreadsKey     = "hide-hyperthreads" + AnnotationDomain
	PickResourcesByHintsKey = "pick-resources-by-hints" + AnnotationDomain
	PreferMemoryTypeKey     = "memory-type" + AnnotationDomain
	ColdStartKey            = "cold-start" + AnnotationDomain
	PreserveCpuKey          = cache.PreserveCpuKey
	PreserveMemoryKey       = cache.PreserveMemoryKey
	BurstableLimitKey       = cache.UnlimitedBurstableKey
	StrictTopologyHintsKey  = cache.StrictTopologyHintsKey

	UnlimitedCpu = math.MaxInt
)

type Preferences struct {
	CpuRequest    int
	CpuLimit      int
	MemoryRequest int64
	MemoryLimit   int64
	ExclusiveCpu  int
	SharedCpu     int

	PreserveCpu      *Preference
	PreserveMemory   *Preference
	ReservedCpus     *Preference
	SharedCpus       *Preference
	IsolatedCpus     *Preference
	StrictIsolated   *Preference
	SchedulingClass  *Preference
	CpuClass         *Preference
	CpuPriority      *Preference
	IrqAffinity      *Preference
	HideHyperthreads *Preference
	PickByHints      *Preference
	StrictHints      *Preference
	MemoryType       *Preference
	BurstableLimit   *Preference
	ColdStart        *Preference
}

// GetContainerPreferences queries the active configuration and annotations
// to determine allocation preferences for the given container.
func (p *policy) GetContainerPreferences(ctr cache.Container) (*Preferences, error) {
	resources, ok := ctr.GetResourceUpdates()
	if !ok {
		resources = ctr.GetResourceRequirements()
	}

	var (
		cpuReq = resources.Requests[corev1.ResourceCPU]
		cpuLim = resources.Limits[corev1.ResourceCPU]
		memReq = resources.Requests[corev1.ResourceMemory]
		memLim = resources.Limits[corev1.ResourceMemory]
	)

	c := &PreferenceCollector{
		policy:   p,
		ctr:      ctr,
		QoSClass: ctr.GetQOSClass(),

		Preferences: &Preferences{
			CpuRequest:    int(cpuReq.MilliValue()),
			CpuLimit:      int(cpuLim.MilliValue()),
			MemoryRequest: int64(memReq.Value()),
			MemoryLimit:   int64(memLim.Value()),
		},
	}

	c.SharedCpu = c.CpuRequest
	if c.QoSClass == corev1.PodQOSGuaranteed && c.SharedCpu%1000 == 0 {
		c.ExclusiveCpu = c.SharedCpu / 1000
		c.SharedCpu = 0
	}

	if err := c.query(); err != nil {
		return nil, err
	}

	if err := c.check(); err != nil {
		return nil, err
	}

	return c.resolve()
}

// Query configuration and effective container annotations for preferences.
func (c *PreferenceCollector) query() error {
	var (
		pref *Preference
		err  error
	)

	for _, a := range PreferenceAnnotations {
		v, scope, ok := c.ctr.QueryEffectiveAnnotation(a.Key)
		if scope == Unscoped {
			scope = PodScope
		}

		pref = nil

		switch {
		case !ok && a.Default != nil:
			pref, err = a.Default(c)
			if err != nil {
				return fmt.Errorf("failed set default preference for %q: %w",
					a.Key, err)
			}
		case ok:
			pref, err = a.Parse(v)
			if err != nil {
				return fmt.Errorf("failed to parse %s preference for %q: %w",
					a.Key, ScopeString(scope), err)
			}
			pref.Scope = scope
		}

		if pref != nil {
			a.Set(c, pref)
		}
	}

	for _, a := range PreferenceAnnotations {
		pref, _ = a.Get(c)

		for _, chk := range a.Check {
			if pref == nil {
				break
			}
			pref, err = chk(c, pref)
			if err != nil {
				return fmt.Errorf("invalid preference %q: %w", a.Key, err)
			}
		}

		a.Set(c, pref)
		log.Debugf("preference set %s = %v", a.Key, pref)
	}

	return nil
}

// Check and flag any semantic conflicts among the preferences.
func (c *PreferenceCollector) check() error {
	var (
		explicitlySet = func(p *Preference) bool {
			return p.GetScope() == ContainerScope
		}
		explicitlyTrue = func(p *Preference) bool {
			return p.BoolValue() && p.GetScope() == ContainerScope
		}
		isSetTrue = func(p *Preference) bool {
			return p.BoolValue() && p.GetScope() < ConfiguredScope
		}
	)

	if explicitlyTrue(c.ReservedCpus) && explicitlyTrue(c.IsolatedCpus) {
		return fmt.Errorf("conflicting preferences: " +
			"both reserved and isolated CPUs preferred")
	}

	if explicitlyTrue(c.SharedCpus) && explicitlyTrue(c.IsolatedCpus) {
		return fmt.Errorf("conflicting preferences: " +
			"both shared and isolated CPUs preferred")
	}

	if isSetTrue(c.ReservedCpus) && isSetTrue(c.IsolatedCpus) {
		if c.ReservedCpus.GetScope() == c.IsolatedCpus.GetScope() {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved and isolated CPUs preferred")
		}
	}

	if isSetTrue(c.SharedCpus) && isSetTrue(c.IsolatedCpus) {
		if c.SharedCpus.GetScope() == c.IsolatedCpus.GetScope() {
			return fmt.Errorf("conflicting preferences: " +
				"both shared and isolated CPUs preferred")
		}
	}

	if isSetTrue(c.ReservedCpus) {
		if explicitlySet(c.SchedulingClass) {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved CPUs preferred and scheduling class is set")
		}
		if explicitlySet(c.CpuClass) {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved CPUs preferred and CPU class is set")
		}
		if explicitlySet(c.CpuPriority) {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved CPUs preferred and CPU priority is set")
		}
		if explicitlySet(c.IrqAffinity) {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved CPUs preferred and IRQ affinity is set")
		}
		if explicitlySet(c.HideHyperthreads) {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved CPUs hyperthread hiding preferred")
		}
		if explicitlySet(c.PickByHints) {
			return fmt.Errorf("conflicting preferences: " +
				"both reserved CPUs and hint-based resource picking preferred")
		}
	}

	if isSetTrue(c.SharedCpus) {
		if explicitlySet(c.SchedulingClass) {
			return fmt.Errorf("conflicting preferences: " +
				"both shared CPUs preferred and scheduling class is set")
		}
		if explicitlySet(c.CpuClass) {
			return fmt.Errorf("conflicting preferences: " +
				"both shared CPUs preferred and CPU class is set")
		}
		if explicitlySet(c.CpuPriority) {
			return fmt.Errorf("conflicting preferences: " +
				"both shared CPUs preferred and CPU priority is set")
		}
		if explicitlySet(c.IrqAffinity) {
			return fmt.Errorf("conflicting preferences: " +
				"both shared CPUs preferred and IRQ affinity is set")
		}
		if explicitlyTrue(c.HideHyperthreads) {
			return fmt.Errorf("conflicting preferences: " +
				"both shared CPUs hyperthread hiding preferred")
		}
		if explicitlyTrue(c.PickByHints) {
			return fmt.Errorf("conflicting preferences: " +
				"both shared CPUs and hint-based resource picking preferred")
		}
	}

	if isSetTrue(c.PreserveCpu) {
		if explicitlySet(c.ReservedCpus) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and reserved CPUs preferred")
		}
		if explicitlySet(c.SharedCpus) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and shared CPUs preferred")
		}
		if explicitlyTrue(c.IsolatedCpus) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and isolated CPUs preferred")
		}
		if explicitlySet(c.SchedulingClass) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and scheduling class is set")
		}
		if explicitlySet(c.CpuClass) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and CPU class is set")
		}
		if explicitlySet(c.CpuPriority) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and CPU priority is set")
		}
		if explicitlySet(c.IrqAffinity) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and IRQ affinity is set")
		}
		if explicitlyTrue(c.HideHyperthreads) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and hyperthread hiding preferred")
		}
		if explicitlyTrue(c.PickByHints) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and hint-based resource picking preferred")
		}
		if explicitlySet(c.BurstableLimit) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve CPU and burstable limit is set")
		}
	}

	if isSetTrue(c.PreserveMemory) {
		if explicitlySet(c.MemoryType) {
			return fmt.Errorf("conflicting preferences: " +
				"both preserve memory and memory type preferred")
		}
	}

	return nil
}

// Resolve the collected preferences to an remaining effective set.
// Suppressed preferences are omitted.
func (c *PreferenceCollector) resolve() (*Preferences, error) {
	p := c.Preferences

	if c.PreserveCpu.BoolValue() || c.PreserveMemory.BoolValue() {
		p.PreserveCpu = c.PreserveCpu
		p.PreserveMemory = c.PreserveMemory
		return p, nil
	}

	if c.ReservedCpus.BoolValue() {
		if c.ReservedCpus.Suppresses(c.IsolatedCpus) {
			p.ReservedCpus = c.ReservedCpus
			p.SharedCpu += 1000 * p.ExclusiveCpu
			p.ExclusiveCpu = 0
			return p, nil
		}
	}

	p.MemoryType = c.MemoryType
	p.ColdStart = c.ColdStart

	if c.SharedCpus.BoolValue() {
		if c.SharedCpus.Suppresses(c.IsolatedCpus) {
			p.SharedCpu += 1000 * p.ExclusiveCpu
			p.ExclusiveCpu = 0
			return p, nil
		}
	}

	switch c.QoSClass {
	case corev1.PodQOSBestEffort:
		return p, nil
	case corev1.PodQOSBurstable:
		p.BurstableLimit = c.BurstableLimit
		return p, nil
	}

	// sub-core allocation
	if c.ExclusiveCpu == 0 {
		return p, nil
	}

	p.SchedulingClass = c.SchedulingClass
	p.CpuClass = c.CpuClass
	p.CpuPriority = c.CpuPriority
	p.IrqAffinity = c.IrqAffinity
	p.HideHyperthreads = c.HideHyperthreads
	p.PickByHints = c.PickByHints

	// for a 'mixed' allocation, we isolate by default
	if 1 <= c.ExclusiveCpu && c.ExclusiveCpu < 2 {
		if c.IsolatedCpus.GetScope() > ConfiguredScope {
			p.IsolatedCpus = Implied(BoolPreference(true))
		} else {
			p.IsolatedCpus = c.IsolatedCpus
		}
		return p, nil
	}

	// for multi-core allocation, isolation needs to be annotated
	if c.IsolatedCpus.GetScope() < ConfiguredScope {
		p.IsolatedCpus = c.IsolatedCpus
	}

	return p, nil
}

type CpuPrio = cfgapi.CPUPriority

const (
	CpuPrioHigh   = cfgapi.PriorityHigh
	CpuPrioNormal = cfgapi.PriorityNormal
	CpuPrioLow    = cfgapi.PriorityLow
	CpuPrioNone   = cfgapi.PriorityNone
)

// Preference represents a single preference.
type Preference struct {
	Value  PreferenceValue
	Scope  PreferenceScope
	Strict bool
}

// PreferenceValue represents one of the possible preference values.
type PreferenceValue struct {
	Bool            bool
	String          string
	SchedulingClass *cfgapi.SchedulingClass
	CpuLevel        cfgapi.CPUTopologyLevel
	CpuPrio         CpuPrio
	MemType         libmem.TypeMask
	Irq             *IrqAffinity
	Duration        time.Duration
}

// PreferenceScope indicates where a preference originated from and
// how specific it is to the container.
type PreferenceScope = cache.AnnotationScope

const (
	ContainerScope  = cache.ContainerScopedAnnotation // container-levle annotation
	PodScope        = cache.PodScopedAnnotation       // pod-level annotation
	Unscoped        = cache.UnscopedAnnotation        // pod-level annotation
	ConfiguredScope = iota                            // policy configuration
	ImpliedScope                                      // implied preference
	UnsetScope                                        // unset preference
)

// Strict returns true if the preference is strict (IOW, required).
func Strict(p *Preference) *Preference {
	if p == nil {
		return nil
	}
	p.Strict = true
	return p
}

// ParseStrict returns a strict version of the parsed preference.
func ParseStrict(fn func(v string) (*Preference, error)) func(string) (*Preference, error) {
	return func(v string) (*Preference, error) {
		pref, err := fn(v)
		if err != nil {
			return nil, err
		}
		if pref != nil {
			pref.Strict = true
		}
		return pref, nil
	}
}

// ScopedPreference return the given preference with its scope set.
// preference.
func ScopedPreference(p *Preference, scope PreferenceScope) *Preference {
	if p == nil {
		return nil
	}
	p.Scope = scope
	return p
}

// Implied returns the given preference with its scope set to implied.
func Implied(p *Preference) *Preference {
	return ScopedPreference(p, ImpliedScope)
}

// Implied returns the given preference with its scope set to configuration.
func Configured(p *Preference) *Preference {
	return ScopedPreference(p, ConfiguredScope)
}

// ParseBoolPreference parses the given string into a boolean preference.
func ParseBoolPreference(str string) (*Preference, error) {
	v, err := strconv.ParseBool(str)
	if err != nil {
		return nil, fmt.Errorf("invalid boolean preference %q: %w", str, err)
	}
	return BoolPreference(v), nil
}

// BoolPreference returns a preference for the given boolean value.
func BoolPreference(v bool) *Preference {
	return &Preference{
		Value: PreferenceValue{
			Bool: v,
		},
	}
}

// BoolPtrPreference returns a preference for the given boolean pointer.
// If the pointer is nil, a nil preference is returned.
func BoolPtrPreference(v *bool) *Preference {
	if v == nil {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			Bool: *v,
		},
	}
}

// ParseCpuPriorityPreference parses the given string into a CPU priority
// preference.
func ParseCpuPriorityPreference(str string) (*Preference, error) {
	p, err := cfgapi.ParseCPUPriority(str)
	if err != nil {
		return nil, err
	}
	if p == CpuPrioNone {
		return nil, nil
	}
	return CpuPrioPreference(p), nil
}

// CpuPrioPreference returns a preference for the given CPU priority value.
// If the value is empty or CpuPrioNone, a nil preference is returned.
func CpuPrioPreference(v CpuPrio) *Preference {
	if v == CpuPrioNone || string(v) == "" {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			CpuPrio: v,
		},
	}
}

// ParseStringPreference parses the given string into a string preference.
func ParseStringPreference(str string) (*Preference, error) {
	return StringPreference(str), nil
}

// StringPreference returns a preference for the given string value.
// If the value is empty, a nil preference is returned.
func StringPreference(v string) *Preference {
	if v == "" {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			String: v,
		},
	}
}

// ParseSchedulingClassPreference parses the given string into a scheduling
// class preference. If the value is nil, a nil preference is returned.
func SchedulingClassPreference(v *cfgapi.SchedulingClass) *Preference {
	if v == nil {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			SchedulingClass: v,
		},
	}
}

// ParseCpuClassPreference parses the given string into a CPU class preference.
func CpuClassPreference(v string) *Preference {
	return StringPreference(v)
}

// ParseCpuLevelPreference parses the given string into a CPU topology level
// preference.
func ParseCpuLevelPreference(str string) (*Preference, error) {
	l := cfgapi.CPUTopologyLevel(str)
	if l.Value() == 0 {
		return nil, fmt.Errorf("invalid CPU topology level %q", str)
	}
	return CpuLevelPreference(l), nil
}

// CpuLevelPreference returns a preference for the given CPU topology level
// value. If the value is undefined, a nil preference is returned.
func CpuLevelPreference(v cfgapi.CPUTopologyLevel) *Preference {
	if v == cfgapi.CPUTopologyLevelUndefined {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			CpuLevel: v,
		},
	}
}

// ParseMemTypePreference parses the given string into a memory type preference.
func ParseMemTypePreference(str string) (*Preference, error) {
	t, err := libmem.ParseTypeMask(str)
	if err != nil {
		return nil, fmt.Errorf("invalid memory type %q: %w", str, err)
	}
	if coldStartOff {
		t |= libmem.TypeMaskDRAM
	}
	return MemTypePreference(t), nil
}

// MemTypePreference returns a preference for the given memory type value. If
// the value is zero, a nil preference is returned.
func MemTypePreference(v libmem.TypeMask) *Preference {
	if v == libmem.TypeMask(0) {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			MemType: v,
		},
	}
}

// ParseIrqAffinityPreference parses the given string into an IRQ affinity
// preference. If the value is nil, a nil preference is returned.
func ParseIrqAffinityPreference(str string) (*Preference, error) {
	irq, err := parseIrqAffinity([]byte(str))
	if err != nil {
		return nil, err
	}
	return IrqAffinityPreference(irq), nil
}

// IrqAffinityPreference returns a preference for the given IRQ affinity value.
// If the value is nil, a nil preference is returned.
func IrqAffinityPreference(v *IrqAffinity) *Preference {
	return &Preference{
		Value: PreferenceValue{
			Irq: v,
		},
	}
}

// ParseDurationPreference parses the given string into a duration preference.
// If the value is zero, a nil preference is returned.
func ParseDurationPreference(str string) (*Preference, error) {
	d, err := time.ParseDuration(str)
	if err != nil {
		return nil, fmt.Errorf("invalid duration preference %q: %w", str, err)
	}
	if d == 0 {
		return nil, nil
	}
	return DurationPreference(d), nil
}

// DurationPreference returns a preference for the given duration value. If the
// value is zero, a nil preference is returned.
func DurationPreference(v time.Duration) *Preference {
	if v == 0 {
		return nil
	}
	return &Preference{
		Value: PreferenceValue{
			Duration: v,
		},
	}
}

// IsStrict returns true if the preference is strict, false otherwise.
// A nil preference is always considered non-strict.
func (p *Preference) IsStrict() bool {
	if p == nil {
		return false
	}
	return p.Strict
}

// GetScope returns the scope of the preference. UnsetScope is returned
// for a nil preference.
func (p *Preference) GetScope() PreferenceScope {
	if p == nil {
		return UnsetScope
	}
	return p.Scope
}

// MoreSpecificThan returns true if the preference is more specific than
// the given one.
func (p *Preference) MoreSpecificThan(scope PreferenceScope) bool {
	return p.GetScope() < scope
}

// Suppresses returns true if the preference suppresses the given one.
func (p *Preference) Suppresses(o *Preference) bool {
	return p.MoreSpecificThan(o.GetScope())
}

// Value returns the boolean value of the preference.
func (p *Preference) BoolValue() bool {
	if p == nil {
		return false
	}
	return p.Value.Bool
}

// StringValue returns the string value of the preference.
func (p *Preference) StringValue() string {
	if p == nil {
		return ""
	}
	return p.Value.String
}

// SchedulingClassValue returns the scheduling class value of the preference.
// For a nil preference nil is returned.
func (p *Preference) SchedulingClassValue() *cfgapi.SchedulingClass {
	if p == nil {
		return nil
	}
	return p.Value.SchedulingClass
}

// CpuLevelValue returns the CPU class value of the preference. For a nil
// preference the undefined topology level is returned.
func (p *Preference) CpuLevelValue() cfgapi.CPUTopologyLevel {
	if p == nil {
		return cfgapi.CPUTopologyLevelUndefined
	}
	return p.Value.CpuLevel
}

// CpuPrioValue returns the CPU priority value of the preference. For a nil
// preference the none priority is returned.
func (p *Preference) CpuPrioValue() CpuPrio {
	if p == nil {
		return CpuPrioNone
	}
	return p.Value.CpuPrio
}

// MemTypeValue returns the memory type value of the preference. For a nil
// preference zero is returned.
func (p *Preference) MemTypeValue() libmem.TypeMask {
	if p == nil {
		return 0
	}
	return p.Value.MemType
}

// IrqAffinityValue returns the IRQ affinity value of the preference. For a nil
// preference nil is returned.
func (p *Preference) IrqAffinityValue() *IrqAffinity {
	if p == nil {
		return nil
	}
	return p.Value.Irq
}

// DurationValue returns the duration value of the preference. For a nil
// preference zero is returned.
func (p *Preference) DurationValue() time.Duration {
	if p == nil {
		return 0
	}
	return p.Value.Duration
}

// String returns a string representation of the preference.
func (p *Preference) String() string {
	if p == nil {
		return "<unset>"
	}

	value := ""
	switch {
	case p.Value.Bool:
		value = strconv.FormatBool(p.Value.Bool)
	case p.Value.String != "":
		value = fmt.Sprintf("%q", p.Value.String)
	case p.Value.SchedulingClass != nil:
		value = fmt.Sprintf("<scheduling-class %s>", p.Value.SchedulingClass.Name)
	case p.Value.CpuLevel.Value() != 0:
		value = fmt.Sprintf("<cpu-level %s>", p.Value.CpuLevel)
	case p.Value.CpuPrio.Value().String() != "none":
		value = fmt.Sprintf("<cpu-prio %s>", string(p.Value.CpuPrio))
	case p.Value.MemType != 0:
		value = fmt.Sprintf("<memory-type %v>", p.Value.MemType)
	case p.Value.Irq != nil:
		value = fmt.Sprintf("<irq-affinity %v>", p.Value.Irq)
	case p.Value.Duration != 0:
		value = fmt.Sprintf("<duration %v>", p.Value.Duration)
	}

	if value == "" {
		return "<unset>"
	}

	scope := ScopeString(p.Scope)
	strict := ""
	if p.Strict {
		strict = " strict"
	} else {
		strict = ""
	}

	return fmt.Sprintf("[%s]%s %s", scope, strict, value)
}

// ScopeString returns a string representation of the preference scope.
func ScopeString(scope PreferenceScope) string {
	switch scope {
	case ContainerScope:
		return "container annotated"
	case PodScope:
		return "pod-default annotated"
	case ConfiguredScope:
		return "configured"
	case ImpliedScope:
		return "implied"
	default:
		return "unset"
	}
}

// PreferenceCollector collects allocation preferences for a container
// querying container annotations and the active policy configuration.
type PreferenceCollector struct {
	policy   *policy
	ctr      cache.Container
	QoSClass corev1.PodQOSClass
	*Preferences

	PreserveCpu      *Preference
	PreserveMemory   *Preference
	ReservedCpus     *Preference
	SharedCpus       *Preference
	IsolatedCpus     *Preference
	SchedulingClass  *Preference
	CpuClass         *Preference
	CpuPriority      *Preference
	IrqAffinity      *Preference
	HideHyperthreads *Preference
	PickByHints      *Preference
	StrictHints      *Preference
	MemoryType       *Preference
	BurstableLimit   *Preference
	ColdStart        *Preference
}

type (
	PrefParse   func(string) (*Preference, error)
	PrefDefault func(*PreferenceCollector) (*Preference, error)
	PrefCheck   func(*PreferenceCollector, *Preference) (*Preference, error)
	PrefGet     func(*PreferenceCollector) (*Preference, error)
	PrefSet     func(*PreferenceCollector, *Preference)
)

// AnnotatablePreference describes a single annotatable preference.
type AnnotatablePreference struct {
	Key     string      // effective annotation key for this preference
	Default PrefDefault // preference value defaults setter
	Parse   PrefParse   // preference value parser
	Check   []PrefCheck // preference value checker(s)
	Get     PrefGet     // preference value getter
	Set     PrefSet     // preference value setter
}

// PreferenceAnnotations defines our known annotations for container
// allocation preferences.
var PreferenceAnnotations = []AnnotatablePreference{
	{
		Key: PreferReservedCpusKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.ReservedCpus, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.ReservedCpus = p
		},
		Default: func(c *PreferenceCollector) (*Preference, error) {
			ns := c.ctr.GetNamespace()
			switch {
			case ns == "kube-system":
				return Implied(BoolPreference(true)), nil
			case c.policy.cfg.IsReservedPoolNamespace(ns):
				return Configured(BoolPreference(true)), nil
			default:
				return nil, nil
			}
		},
		Parse: ParseBoolPreference,
	},
	{
		Key: PreferSharedCpusKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.SharedCpus, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.SharedCpus = p
		},
		Default: func(c *PreferenceCollector) (*Preference, error) {
			return Configured(BoolPtrPreference(c.policy.cfg.PreferShared)), nil
		},
		Parse: ParseBoolPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: PreferIsolatedCpusKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.IsolatedCpus, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.IsolatedCpus = p
		},
		Default: func(c *PreferenceCollector) (*Preference, error) {
			return Configured(BoolPtrPreference(c.policy.cfg.PreferIsolated)), nil
		},
		Parse: ParseBoolPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: RequireIsolatedCpusKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			if c.IsolatedCpus.IsStrict() {
				return c.IsolatedCpus, nil
			}
			return nil, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			if p.GetScope() <= c.IsolatedCpus.GetScope() {
				c.IsolatedCpus = p
			}
		},
		Parse: ParseStrict(ParseBoolPreference),
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: HideHyperthreadsKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.HideHyperthreads, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.HideHyperthreads = p
		},
		Parse: ParseBoolPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: PickResourcesByHintsKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.PickByHints, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.PickByHints = p
		},
		Parse: ParseBoolPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: StrictTopologyHintsKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.StrictHints, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.StrictHints = p
		},
		Parse: ParseStrict(ParseBoolPreference),
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: PreserveCpuKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.PreserveCpu, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.PreserveCpu = p
		},
		Parse: ParseBoolPreference,
	},
	{
		Key: PreserveMemoryKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.PreserveMemory, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.PreserveMemory = p
		},
		Parse: ParseBoolPreference,
	},
	{
		Key: SchedulingClassKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.SchedulingClass, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.SchedulingClass = p
		},
		Default: func(c *PreferenceCollector) (*Preference, error) {
			sc, err := c.policy.cfg.GetNamespaceSchedulingClass(c.ctr.GetNamespace())
			if err != nil {
				return nil, err
			}
			if sc == nil {
				sc, err = c.policy.cfg.GetPodQoSSchedulingClass(c.QoSClass)
				if err != nil {
					return nil, err
				}
			}
			return Configured(SchedulingClassPreference(sc)), nil
		},
		Parse: ParseStringPreference,
		Check: []PrefCheck{
			func(c *PreferenceCollector, p *Preference) (*Preference, error) {
				name := p.StringValue()
				if name == "" {
					return nil, nil
				}
				sc := c.policy.cfg.GetSchedulingClass(name)
				if sc == nil {
					return nil, fmt.Errorf("unknown scheduling class %q", name)
				}
				return ScopedPreference(SchedulingClassPreference(sc), p.Scope), nil
			},
		},
	},
	{
		Key: CpuClassKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.CpuClass, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.CpuClass = p
		},
		Default: func(c *PreferenceCollector) (*Preference, error) {
			if c.QoSClass != corev1.PodQOSGuaranteed {
				return nil, nil
			}
			if c.ExclusiveCpu == 0 {
				return nil, nil
			}
			return Configured(
				CpuClassPreference(c.policy.cfg.DefaultExclusiveCpuClass),
			), nil
		},
		Parse: ParseStringPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
			func(c *PreferenceCollector, p *Preference) (*Preference, error) {
				class := p.StringValue()
				if class == "" {
					return nil, nil
				}
				if !c.policy.cpuClasses.IsKnownClass(class) {
					return nil, fmt.Errorf("unknown CPU class %q", class)
				}
				return ScopedPreference(CpuClassPreference(class), p.Scope), nil
			},
		},
	},
	{
		Key: IrqAffinityKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.IrqAffinity, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.IrqAffinity = p
		},

		Parse: ParseIrqAffinityPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
			func(c *PreferenceCollector, p *Preference) (*Preference, error) {
				if p == nil {
					return nil, nil
				}
				irq, hints := p.Value.Irq, c.ctr.GetTopologyHints()
				if err := addIrqAffinityForHints(irq, hints); err != nil {
					return nil, err
				}
				return p, nil
			},
		},
	},
	{
		Key: PreferCpuPriorityKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.CpuPriority, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.CpuPriority = p
		},
		Default: func(c *PreferenceCollector) (p *Preference, err error) {
			return Configured(CpuPrioPreference(c.policy.cfg.DefaultCPUPriority)), nil
		},
		Parse: ParseCpuPriorityPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSGuaranteed),
		},
	},
	{
		Key: BurstableLimitKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.BurstableLimit, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.BurstableLimit = p
		},

		Default: func(c *PreferenceCollector) (*Preference, error) {
			if c.QoSClass != corev1.PodQOSBurstable {
				return nil, nil
			}
			if c.CpuLimit != 0 {
				return nil, nil
			}
			return Configured(CpuLevelPreference(c.policy.cfg.UnlimitedBurstable)), nil
		},
		Parse: ParseCpuLevelPreference,
		Check: []PrefCheck{
			AcceptQoS(corev1.PodQOSBurstable),
			func(c *PreferenceCollector, p *Preference) (*Preference, error) {
				if c.CpuLimit != 0 && p.Scope == ContainerScope {
					return nil,
						fmt.Errorf("invalid burstable limit %v: "+
							"container has CPU limit %d", p.Value.CpuLevel, c.CpuLimit)
				}
				return p, nil
			},
		},
	},
	{
		Key: PreferMemoryTypeKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.MemoryType, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			c.MemoryType = p
		},
		Parse: ParseMemTypePreference,
	},
	{
		Key: ColdStartKey,
		Get: func(c *PreferenceCollector) (*Preference, error) {
			return c.ColdStart, nil
		},
		Set: func(c *PreferenceCollector, p *Preference) {
			if !coldStartOff {
				c.ColdStart = p
			}
		},
		Parse: ParseDurationPreference,
	},
}

// AcceptQoS returns a preference check function for the given QoS classes
func AcceptQoS(qos corev1.PodQOSClass) func(*PreferenceCollector, *Preference) (*Preference, error) {
	return func(c *PreferenceCollector, pref *Preference) (*Preference, error) {
		scope := pref.GetScope()
		switch {
		case scope == ContainerScope:
			if c.QoSClass != qos {
				return nil, fmt.Errorf("only valid for %s QoS class (not for %s)",
					string(qos), string(c.QoSClass))
			}
			return pref, nil
		case c.QoSClass != qos:
			return nil, nil
		}
		return pref, nil
	}
}

func init() {
	// Check that our preference setters and getters are consistently set up.
	c := &PreferenceCollector{}
	for _, a := range PreferenceAnnotations {
		pref := &Preference{
			Value: PreferenceValue{
				Bool: true,
			},
			Scope:  ContainerScope,
			Strict: true,
		}
		a.Set(c, pref)
		p, err := a.Get(c)
		if err != nil {
			panic(fmt.Sprintf("preference getter test failed for %q: %v", a.Key, err))
		}
		if p != pref {
			panic(fmt.Sprintf("preference getter test failed for %q", a.Key))
		}
	}
}
