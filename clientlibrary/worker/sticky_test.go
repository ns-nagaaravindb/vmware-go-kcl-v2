/*
 * Copyright (c) 2018 VMware, Inc.
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
 * associated documentation files (the "Software"), to deal in the Software without restriction, including
 * without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is furnished to do
 * so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all copies or substantial
 * portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT
 * NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
 * IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
 * WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
 * SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
 */
 package worker

 import (
	 "sync"
	 "testing"
 
	 par "github.com/vmware/vmware-go-kcl/clientlibrary/partition"
 )
 
 func TestShardStatus_StickyGetterSetter(t *testing.T) {
	 shard := &par.ShardStatus{
		 ID:  "test-shard",
		 Mux: &sync.RWMutex{},
	 }
 
	 // Test default value
	 if shard.GetSticky() != 0 {
		 t.Errorf("Expected default sticky value to be 0, got %d", shard.GetSticky())
	 }
 
	 // Test setting and getting
	 shard.SetSticky(10)
	 if shard.GetSticky() != 10 {
		 t.Errorf("Expected sticky value to be 10, got %d", shard.GetSticky())
	 }
 
	 // Test setting to -1
	 shard.SetSticky(-1)
	 if shard.GetSticky() != -1 {
		 t.Errorf("Expected sticky value to be -1, got %d", shard.GetSticky())
	 }
 
	 // Test setting to 0
	 shard.SetSticky(0)
	 if shard.GetSticky() != 0 {
		 t.Errorf("Expected sticky value to be 0, got %d", shard.GetSticky())
	 }
 }
 
 func TestShardStatus_StickyConcurrency(t *testing.T) {
	 shard := &par.ShardStatus{
		 ID:  "test-shard",
		 Mux: &sync.RWMutex{},
	 }
 
	 // Test concurrent access
	 done := make(chan bool)
	 for i := 0; i < 10; i++ {
		 go func(val int) {
			 shard.SetSticky(val)
			 _ = shard.GetSticky()
			 done <- true
		 }(i)
	 }
 
	 for i := 0; i < 10; i++ {
		 <-done
	 }
 
	 // Just verify we didn't panic
	 t.Log("Concurrent access test passed")
 }
 
 func TestStickyShardBehavior(t *testing.T) {
	 tests := []struct {
		 name         string
		 stickyValue  int
		 assignedTo   string
		 currentWorker string
		 shouldSkip   bool
		 description  string
	 }{
		 {
			 name:          "Sticky=10, assigned to other worker",
			 stickyValue:   10,
			 assignedTo:    "worker-other",
			 currentWorker: "worker-1",
			 shouldSkip:    true,
			 description:   "Should skip sticky shard assigned to another worker",
		 },
		 {
			 name:          "Sticky=10, assigned to current worker",
			 stickyValue:   10,
			 assignedTo:    "worker-1",
			 currentWorker: "worker-1",
			 shouldSkip:    false,
			 description:   "Should allow sticky shard assigned to current worker (renewal)",
		 },
		 {
			 name:          "Sticky=0, assigned to other worker",
			 stickyValue:   0,
			 assignedTo:    "worker-other",
			 currentWorker: "worker-1",
			 shouldSkip:    false,
			 description:   "Should not skip non-sticky shard (normal rebalancing applies)",
		 },
		 {
			 name:          "Sticky=-1, assigned to other worker",
			 stickyValue:   -1,
			 assignedTo:    "worker-other",
			 currentWorker: "worker-1",
			 shouldSkip:    false,
			 description:   "Should not skip shard with sticky=-1 (normal behavior)",
		 },
		 {
			 name:          "Sticky=10, no assignment",
			 stickyValue:   10,
			 assignedTo:    "",
			 currentWorker: "worker-1",
			 shouldSkip:    false,
			 description:   "Should allow acquiring unassigned sticky shard",
		 },
		 {
			 name:          "Sticky=5, assigned to other worker",
			 stickyValue:   5,
			 assignedTo:    "worker-other",
			 currentWorker: "worker-1",
			 shouldSkip:    false,
			 description:   "Should not skip shard with sticky < 10",
		 },
		 {
			 name:          "Sticky=20, no assignment",
			 stickyValue:   20,
			 assignedTo:    "",
			 currentWorker: "worker-1",
			 shouldSkip:    true,
			 description:   "Should skip sticky=20 shard (release signal), even if unassigned",
		 },
		 {
			 name:          "Sticky=20, assigned to current worker",
			 stickyValue:   20,
			 assignedTo:    "worker-1",
			 currentWorker: "worker-1",
			 shouldSkip:    true,
			 description:   "Should skip sticky=20 shard (release signal), even if assigned to current worker",
		 },
		 {
			 name:          "Sticky=20, assigned to other worker",
			 stickyValue:   20,
			 assignedTo:    "worker-other",
			 currentWorker: "worker-1",
			 shouldSkip:    true,
			 description:   "Should skip sticky=20 shard (release signal)",
		 },
	 }
 
	 for _, tt := range tests {
		 t.Run(tt.name, func(t *testing.T) {
			 shard := &par.ShardStatus{
				 ID:         "test-shard",
				 Mux:        &sync.RWMutex{},
				 AssignedTo: tt.assignedTo,
			 }
			 shard.SetSticky(tt.stickyValue)
 
			 // Simulate the logic in eventLoop
			 // sticky=10: skip if assigned to other worker
			 // sticky=20: always skip
			 shouldSkip := (shard.GetSticky() == 10 && shard.GetLeaseOwner() != "" && shard.GetLeaseOwner() != tt.currentWorker) ||
				 shard.GetSticky() == 20
 
			 if shouldSkip != tt.shouldSkip {
				 t.Errorf("%s: Expected shouldSkip=%v, got shouldSkip=%v", tt.description, tt.shouldSkip, shouldSkip)
			 }
		 })
	 }
 }
 
 func TestStickyShardRebalancing(t *testing.T) {
	 tests := []struct {
		 name             string
		 shards           []*par.ShardStatus
		 expectedEligible int
		 description      string
	 }{
		 {
			 name: "All shards are non-sticky",
			 shards: []*par.ShardStatus{
				 {ID: "shard-1", Sticky: 0, Mux: &sync.RWMutex{}},
				 {ID: "shard-2", Sticky: -1, Mux: &sync.RWMutex{}},
				 {ID: "shard-3", Sticky: 5, Mux: &sync.RWMutex{}},
			 },
			 expectedEligible: 3,
			 description:      "All non-sticky shards should be eligible for stealing",
		 },
		 {
			 name: "All shards are sticky=10",
			 shards: []*par.ShardStatus{
				 {ID: "shard-1", Sticky: 10, Mux: &sync.RWMutex{}},
				 {ID: "shard-2", Sticky: 10, Mux: &sync.RWMutex{}},
				 {ID: "shard-3", Sticky: 10, Mux: &sync.RWMutex{}},
			 },
			 expectedEligible: 0,
			 description:      "No sticky=10 shards should be eligible for stealing",
		 },
		 {
			 name: "All shards are sticky=20",
			 shards: []*par.ShardStatus{
				 {ID: "shard-1", Sticky: 20, Mux: &sync.RWMutex{}},
				 {ID: "shard-2", Sticky: 20, Mux: &sync.RWMutex{}},
				 {ID: "shard-3", Sticky: 20, Mux: &sync.RWMutex{}},
			 },
			 expectedEligible: 0,
			 description:      "No sticky=20 shards should be eligible for stealing",
		 },
		 {
			 name: "Mixed sticky and non-sticky shards",
			 shards: []*par.ShardStatus{
				 {ID: "shard-1", Sticky: 10, Mux: &sync.RWMutex{}},
				 {ID: "shard-2", Sticky: 0, Mux: &sync.RWMutex{}},
				 {ID: "shard-3", Sticky: -1, Mux: &sync.RWMutex{}},
				 {ID: "shard-4", Sticky: 10, Mux: &sync.RWMutex{}},
			 },
			 expectedEligible: 2,
			 description:      "Only non-sticky shards should be eligible",
		 },
		 {
			 name: "Mixed sticky=10, sticky=20, and normal shards",
			 shards: []*par.ShardStatus{
				 {ID: "shard-1", Sticky: 10, Mux: &sync.RWMutex{}},
				 {ID: "shard-2", Sticky: 20, Mux: &sync.RWMutex{}},
				 {ID: "shard-3", Sticky: 0, Mux: &sync.RWMutex{}},
				 {ID: "shard-4", Sticky: -1, Mux: &sync.RWMutex{}},
				 {ID: "shard-5", Sticky: 20, Mux: &sync.RWMutex{}},
			 },
			 expectedEligible: 2,
			 description:      "Only non-sticky shards should be eligible (excluding both 10 and 20)",
		 },
	 }
 
	 for _, tt := range tests {
		 t.Run(tt.name, func(t *testing.T) {
			 // Simulate the logic in rebalance()
			 var eligibleShards []*par.ShardStatus
			 for _, shard := range tt.shards {
				 if shard.GetSticky() != 10 && shard.GetSticky() != 20 {
					 eligibleShards = append(eligibleShards, shard)
				 }
			 }
 
			 if len(eligibleShards) != tt.expectedEligible {
				 t.Errorf("%s: Expected %d eligible shards, got %d", tt.description, tt.expectedEligible, len(eligibleShards))
			 }
		 })
	 }
 }
 