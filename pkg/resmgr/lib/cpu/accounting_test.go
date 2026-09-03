package libcpu

import (
	"fmt"
	"testing"
)

type testPool struct {
	name string
	cpus *CpuMask
}

func (p *testPool) String() string {
	return fmt.Sprintf("%s<%s>", p.name, p.cpus)
}

type testUser struct {
	name      string
	id        string
	exclusive *CpuMask
	shared    *CpuMask
	usage     int
}

func (u *testUser) Name() string {
	return u.name
}

func (u *testUser) ID() string {
	if u.id != "" {
		return u.id
	}
	return u.name
}

func (u *testUser) ExclusiveCpus() *CpuMask {
	return u.exclusive
}

func (u *testUser) SharedCpus() *CpuMask {
	return u.shared
}

func (u *testUser) SharedUsage() int {
	return u.usage
}

func (u *testUser) String() string {
	exclusive := ""
	shared := ""

	if !u.exclusive.IsEmpty() {
		exclusive = fmt.Sprintf("%s", u.exclusive)
	}
	if u.usage > 0 {
		if exclusive != "" {
			shared = "+"
		}
		shared += fmt.Sprintf("%dm of %s", u.usage, u.shared)
	}

	return fmt.Sprintf("%s{%s%s}", u.name, exclusive, shared)
}

func TestAccounting(t *testing.T) {
	testPools := map[string]*testPool{
		"root":           {name: "root", cpus: NewCpuMask(0, 1, 2, 3, 4, 5, 6, 7)},
		"node #0":        {name: "node #0", cpus: NewCpuMask(0, 1)},
		"node #1":        {name: "node #1", cpus: NewCpuMask(2, 3)},
		"node #2":        {name: "node #2", cpus: NewCpuMask(4, 5)},
		"node #3":        {name: "node #3", cpus: NewCpuMask(6, 7)},
		"socket #0":      {name: "socket #0", cpus: NewCpuMask(0, 1, 2, 3)},
		"socket #1":      {name: "socket #1", cpus: NewCpuMask(4, 5, 6, 7)},
		"pseudo-node #4": {name: "pseudo-node #4", cpus: NewCpuMask(3, 4)},
	}

	testUsers := map[string][]*testUser{
		"node #0": {
			{name: "container #0", usage: 750},
			{name: "container #1", usage: 250},
			{name: "container #2", usage: 500},
		},
		"node #1": {
			{name: "container #3", usage: 1000},
			{name: "container #4", usage: 500},
			{name: "container #5", usage: 250},
			//{name: "container #6", usage: 250},
		},
		"node #2": {
			{name: "container #7", usage: 1000},
			{name: "container #8", usage: 750},
		},
		"node #3": {
			{name: "container #9", usage: 500},
			{name: "container #10", usage: 500},
			//{name: "container #11", usage: 150},
		},
		"socket #0": {
			{name: "container #12", usage: 250},
			{name: "container #13", usage: 250},
		},
		"socket #1": {
			{name: "container #14", usage: 500},
			{name: "container #15", usage: 250},
		},
		"root": {
			{name: "container #16", usage: 250},
			{name: "container #17", usage: 100},
		},
		"pseudo-node #4": {
			{name: "container #18", usage: 250},
			{name: "container #19", usage: 150},
		},
	}

	for name, users := range testUsers {
		p, ok := testPools[name]
		if !ok {
			panic("unknown pool " + name)
		}
		for _, u := range users {
			u.exclusive = NewCpuMask()
			u.shared = p.cpus.Clone().(*CpuMask)
		}
	}

	a := NewAccounting()

	for pool, users := range testUsers {
		for _, u := range users {
			if err := a.Add(u); err != nil {
				fmt.Printf("Failed to account user %q to tree pool %q: %v\n",
					u.Name(), pool, err)
			} else {
				fmt.Printf("%s: accounted for %s\n", testPools[pool], u)
			}

			for _, p := range testPools {
				fmt.Printf("= %s: available CPU %d\n", p, a.Available(p.cpus))
			}

		}
	}

	for pool, users := range testUsers {
		for _, u := range users {
			if err := a.Del(u); err != nil {
				fmt.Printf("Failed to release user %q: %v\n", u.Name(), err)
			} else {
				fmt.Printf("%s: dismissed %s\n", testPools[pool], u)
			}

			for _, p := range testPools {
				fmt.Printf("= %s: available CPU %d\n", p, a.Available(p.cpus))
			}

		}
	}
}
