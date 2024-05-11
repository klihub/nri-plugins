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
	logger "github.com/containers/nri-plugins/pkg/log"
	idset "github.com/intel/goresctrl/pkg/utils"
)

type (
	ID    = idset.ID
	IDSet = idset.IDSet
	Nodes = NodeMask
	Kinds = KindMask
)

var (
	NewIDSet = idset.NewIDSet
	log      = logger.Get("libmem")
)

// Request is a request to allocate some memory to a workload.
type Request struct {
	Workload string   // ID of workload this offer belongs to
	Amount   int64    // amount of memory requested
	Kinds    KindMask // kind of memory requested
	Nodes    NodeMask // nodes to start allocating memory from
}

// Offer represents potential allocation of some memory to a workload.
//
// An offer contains all the changes, including potential changes to
// other workloads, which are necessary to fulfill the allocation.
// An offer can be committed into an allocation provided that no new
// allocation has taken place since the offer was created. Offers can
// be used to compare various allocation alternatives for a workload.
type Offer struct {
	request  *Request            // allocation request
	updates  map[string]NodeMask // updated workloads to fulfill the request
	validity int64               // validity of this offer (wrt. Allocator.generation)
}

// Allocation represents some memory allocated to a workload.
type Allocation struct {
	request *Request // allocation request
	nodes   NodeMask // nodes allocated to fulfill this request
}

func (r *Request) Clone() *Request {
	return &Request{
		Workload: r.Workload,
		Amount:   r.Amount,
		Kinds:    r.Kinds,
		Nodes:    r.Nodes,
	}
}
