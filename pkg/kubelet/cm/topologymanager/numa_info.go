/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package topologymanager

import (
	"fmt"
	"runtime"
	"slices"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
)

type NUMADistances map[int][]uint64

type NUMAInfo struct {
	Nodes         []int
	NUMADistances NUMADistances
}

func NewNUMAInfo(topology []cadvisorapi.Node, opts PolicyOptions) (*NUMAInfo, error) {
	return newNUMAInfo(topology, opts, false)
}

func newNUMAInfo(topology []cadvisorapi.Node, opts PolicyOptions, independent bool) (*NUMAInfo, error) {
	var numaNodes []int
	distances := map[int][]uint64{}
	for _, node := range topology {
		numaNodes = append(numaNodes, node.Id)

		var nodeDistance []uint64
		if opts.PreferClosestNUMA && !independent {
			nodeDistance = node.Distances
			if nodeDistance == nil {
				if runtime.GOOS == "windows" {
					return nil, fmt.Errorf("the %q policy option is not supported on Windows because NUMA node distances are not available", PreferClosestNUMANodes)
				}
				return nil, fmt.Errorf("error getting NUMA distances from cadvisor")
			}
		}
		if independent {
			// The independent planner validates distance data only when ranking
			// is necessary. Explicit or restored pairs must work without it.
			nodeDistance = slices.Clone(node.Distances)
		}
		distances[node.Id] = nodeDistance
	}

	numaInfo := &NUMAInfo{
		Nodes:         numaNodes,
		NUMADistances: distances,
	}

	return numaInfo, nil
}

// closestPlacement ranks feasible pairs in the CPU-to-memory direction. Either
// side may vary, so CPU-only requests can use the same ordering as memory-only
// requests. Unknown distances cannot remove a feasible candidate from the race.
func (n *NUMAInfo) closestPlacement(candidates []NUMAPlacement) (NUMAPlacement, error) {
	if len(candidates) == 0 {
		return NUMAPlacement{}, fmt.Errorf("no feasible singleton CPU node with sufficient capacity and compatible device topology")
	}
	for _, candidate := range candidates {
		if candidate.CPU == candidate.Memory {
			return candidate, nil
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	best := candidates[0]
	var bestDistance uint64
	for i, candidate := range candidates {
		distance, err := n.cpuToMemoryDistance(candidate.CPU, candidate.Memory)
		if err != nil {
			return NUMAPlacement{}, fmt.Errorf("cannot rank CPU-to-memory NUMA placement: %w; specify both cpuNUMA and memNUMA or correct topology distance data", err)
		}
		if i == 0 || distance < bestDistance || distance == bestDistance && (candidate.CPU < best.CPU || candidate.CPU == best.CPU && candidate.Memory < best.Memory) {
			best, bestDistance = candidate, distance
		}
	}
	return best, nil
}

func (n *NUMAInfo) cpuToMemoryDistance(cpu, memory int) (uint64, error) {
	// Linux node_read_distance emits one column per online node in ascending
	// ID order; cAdvisor preserves those columns, not discovery slice order.
	// https://github.com/torvalds/linux/blob/master/drivers/base/node.c
	nodes := slices.Clone(n.Nodes)
	slices.Sort(nodes)
	row := n.NUMADistances[cpu]
	if len(row) != len(nodes) {
		return 0, fmt.Errorf("NUMA node %d has %d distance columns, expected %d", cpu, len(row), len(nodes))
	}
	column := -1
	for i, id := range nodes {
		if id < 0 || id > 63 || i > 0 && id == nodes[i-1] {
			return 0, fmt.Errorf("NUMA node IDs are invalid or ambiguous: %v", nodes)
		}
		if row[i] < 10 || row[i] >= 255 || id == cpu && row[i] != 10 {
			return 0, fmt.Errorf("unusable NUMA distance from node %d to node %d: %d", cpu, id, row[i])
		}
		if id == memory {
			column = i
		}
	}
	if !slices.Contains(nodes, cpu) || column == -1 {
		return 0, fmt.Errorf("NUMA pair (%d, %d) is absent from topology", cpu, memory)
	}
	return row[column], nil
}

func (n *NUMAInfo) Narrowest(m1 bitmask.BitMask, m2 bitmask.BitMask) bitmask.BitMask {
	if m1.IsNarrowerThan(m2) {
		return m1
	}
	return m2
}

func (n *NUMAInfo) Closest(m1 bitmask.BitMask, m2 bitmask.BitMask) bitmask.BitMask {
	// If the length of both bitmasks aren't the same, choose the one that is narrowest.
	if m1.Count() != m2.Count() {
		return n.Narrowest(m1, m2)
	}

	m1Distance := n.NUMADistances.CalculateAverageFor(m1)
	m2Distance := n.NUMADistances.CalculateAverageFor(m2)
	// If average distance is the same, take bitmask with more lower-number bits set.
	if m1Distance == m2Distance {
		if m1.IsLessThan(m2) {
			return m1
		}
		return m2
	}

	// Otherwise, return the bitmask with the shortest average distance between NUMA nodes.
	if m1Distance < m2Distance {
		return m1
	}

	return m2
}

func (n NUMAInfo) DefaultAffinityMask() bitmask.BitMask {
	defaultAffinity, _ := bitmask.NewBitMask(n.Nodes...)
	return defaultAffinity
}

func (d NUMADistances) CalculateAverageFor(bm bitmask.BitMask) float64 {
	// This should never happen, but just in case make sure we do not divide by zero.
	if bm.Count() == 0 {
		return 0
	}

	var count float64 = 0
	var sum float64 = 0
	for _, node1 := range bm.GetBits() {
		for _, node2 := range bm.GetBits() {
			sum += float64(d[node1][node2])
			count++
		}
	}

	return sum / count
}
