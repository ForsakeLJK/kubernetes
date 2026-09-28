/*
Copyright 2026 The Kubernetes Authors.

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

package memorymanager

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/kubernetes/test/utils/ktesting"
)

func TestNUMAPlacementRefusesRestoredMemoryAssignment(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(2)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("restored-pod")}, Spec: v1.PodSpec{
		NUMANode:   &node,
		Containers: []v1.Container{{Name: "app"}},
	}}
	memoryState := state.NewMemoryState(logger)
	blocks := []state.Block{{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 1024}}
	memoryState.SetMemoryBlocks(string(pod.UID), "app", blocks)
	m := &manager{state: memoryState}
	container := &pod.Spec.Containers[0]
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("restored assignment was not refused: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err == nil {
		t.Fatal("restored assignment could reach runtime configuration")
	}
	if actual := memoryState.GetMemoryBlocks(string(pod.UID), container.Name); len(actual) != 1 || actual[0].NUMAAffinity[0] != 2 {
		t.Fatalf("restored memory assignment was modified: %+v", actual)
	}
}

func TestNUMAPlacementRollbackRestoresInitReuse(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(2)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("rollback-pod")}, Spec: v1.PodSpec{
		NUMANode: &node, InitContainers: []v1.Container{{Name: "init"}}, Containers: []v1.Container{{Name: "app"}},
	}}
	const gib = uint64(1024 * 1024 * 1024)
	memoryState := state.NewMemoryState(logger)
	hugepages := v1.ResourceName(v1.ResourceHugePagesPrefix + "1Gi")
	memoryState.SetMachineState(state.NUMANodeMap{2: {MemoryMap: map[v1.ResourceName]*state.MemoryTable{
		v1.ResourceMemory: {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: gib, Reserved: gib},
		hugepages:         {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: gib, Reserved: gib},
	}, NumberOfAssignments: 2}})
	memoryState.SetMemoryBlocks(string(pod.UID), "init", []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: gib},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: gib},
	})
	policy := &staticPolicy{initContainersReusableMemory: map[string]map[string]map[v1.ResourceName]uint64{string(pod.UID): {"2": {v1.ResourceMemory: gib, hugepages: gib}}}}
	m := &manager{state: memoryState, policy: policy, numaPlacementAllocations: map[string]bool{numaPlacementKey(pod, &pod.Spec.InitContainers[0]): true}}
	rollback := m.SnapshotNUMAPlacement(pod, &pod.Spec.Containers[0])
	memoryState.SetMachineState(state.NUMANodeMap{2: {MemoryMap: map[v1.ResourceName]*state.MemoryTable{
		v1.ResourceMemory: {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: 0, Reserved: 2 * gib},
		hugepages:         {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: 0, Reserved: 2 * gib},
	}, NumberOfAssignments: 4}})
	memoryState.SetMemoryBlocks(string(pod.UID), "init", []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 0},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: 0},
	})
	memoryState.SetMemoryBlocks(string(pod.UID), "app", []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 2 * gib},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: 2 * gib},
	})
	policy.initContainersReusableMemory[string(pod.UID)]["2"][v1.ResourceMemory] = 0
	policy.initContainersReusableMemory[string(pod.UID)]["2"][hugepages] = 0
	m.numaPlacementAllocations[numaPlacementKey(pod, &pod.Spec.Containers[0])] = true
	if err := rollback(klog.Background()); err != nil {
		t.Fatal(err)
	}
	if blocks := memoryState.GetMemoryBlocks(string(pod.UID), "init"); len(blocks) != 2 || blocks[0].Size != gib || blocks[1].Size != gib {
		t.Fatalf("init assignment was not restored: %+v", blocks)
	}
	if blocks := memoryState.GetMemoryBlocks(string(pod.UID), "app"); blocks != nil {
		t.Fatalf("new application assignment retained: %+v", blocks)
	}
	if nodeState := memoryState.GetMachineState()[2]; nodeState.MemoryMap[v1.ResourceMemory].Free != gib || nodeState.MemoryMap[hugepages].Free != gib || nodeState.NumberOfAssignments != 2 {
		t.Fatalf("node accounting was not restored: %+v", nodeState)
	}
	if got := policy.initContainersReusableMemory[string(pod.UID)]["2"][v1.ResourceMemory]; got != gib {
		t.Fatalf("reusable memory = %d, want %d", got, gib)
	}
	if got := policy.initContainersReusableMemory[string(pod.UID)]["2"][hugepages]; got != gib {
		t.Fatalf("reusable hugepages = %d, want %d", got, gib)
	}
	if m.numaPlacementAllocations[numaPlacementKey(pod, &pod.Spec.Containers[0])] {
		t.Fatal("new application allocation remained committed")
	}
	policy.RemoveContainer(logger, memoryState, string(pod.UID), "init")
	if blocks := memoryState.GetMemoryBlocks(string(pod.UID), "init"); blocks != nil {
		t.Fatalf("normal cleanup retained init blocks: %+v", blocks)
	}
	if nodeState := memoryState.GetMachineState()[2]; nodeState.MemoryMap[v1.ResourceMemory].Free != 2*gib || nodeState.MemoryMap[hugepages].Free != 2*gib || nodeState.NumberOfAssignments != 0 {
		t.Fatalf("normal cleanup did not release ordinary memory and hugepages: %+v", nodeState)
	}
}
