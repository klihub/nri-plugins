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

package libmem

import (
	"fmt"
	"strings"

	system "github.com/containers/nri-plugins/pkg/sysfs"
)

// Kind describes known types of memory.
type Kind int

const (
	// Ordinary DRAM.
	KindDRAM Kind = iota
	// Persistent memory (typically available in high capacity).
	KindPMEM
	// High-bandwidth memory (typically available in low capacity).
	KindHBM

	KindMax
)

var (
	typeToKind = map[system.MemoryType]Kind{
		system.MemoryTypeDRAM: KindDRAM,
		system.MemoryTypePMEM: KindPMEM,
		system.MemoryTypeHBM:  KindHBM,
	}
	kindToString = map[Kind]string{
		KindDRAM: "DRAM",
		KindPMEM: "PMEM",
		KindHBM:  "HB-MEM",
	}
)

func TypeToKind(t system.MemoryType) Kind {
	if k, ok := typeToKind[t]; ok {
		return k
	}
	panic(fmt.Errorf("can't provide Kind for unknown memory type %v", t))
}

func (k Kind) String() string {
	if s, ok := kindToString[k]; ok {
		return s
	}
	return fmt.Sprintf("%%!(BAD-memory.Kind:%d)", k)
}

// KindMask describes a set of memory Kinds
type KindMask int

const (
	KindMaskAll = KindMask((1 << KindMax) - 1)
)

func MaskForKinds(kinds ...Kind) KindMask {
	mask := KindMask(0)
	for _, k := range kinds {
		mask |= (1 << k)
	}
	return mask
}

func MaskForTypes(types ...system.MemoryType) KindMask {
	mask := KindMask(0)
	for _, t := range types {
		mask |= (1 << TypeToKind(t))
	}
	return mask
}

func (m KindMask) Clone() *KindMask {
	c := m
	return &c
}

func (m KindMask) Slice() []Kind {
	var kinds []Kind
	if m.Has(KindDRAM) {
		kinds = append(kinds, KindDRAM)
	}
	if m.Has(KindPMEM) {
		kinds = append(kinds, KindPMEM)
	}
	if m.Has(KindHBM) {
		kinds = append(kinds, KindHBM)
	}
	return kinds
}

func (m *KindMask) Set(kinds ...Kind) *KindMask {
	for _, k := range kinds {
		(*m) |= (1 << k)
	}
	return m
}

func (m *KindMask) Clear(kinds ...Kind) *KindMask {
	for _, k := range kinds {
		(*m) &^= (1 << k)
	}
	return m
}

func (m KindMask) Has(kinds ...Kind) bool {
	for _, k := range kinds {
		if (m & (1 << k)) == 0 {
			return false
		}
	}
	return true
}

func (m KindMask) IsEmpty() bool {
	return m == 0
}

func (m KindMask) String() string {
	kinds := []string{}
	for k := KindDRAM; k < KindMax; k++ {
		if m.Has(k) {
			kinds = append(kinds, k.String())
		}
	}
	return "{" + strings.Join(kinds, ",") + "}"
}
