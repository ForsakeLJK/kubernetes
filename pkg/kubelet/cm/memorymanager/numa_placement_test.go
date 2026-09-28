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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/test/utils/ktesting"
)

func TestNUMAPlacementRestoredMemoryAssignment(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(2)
	hugepages := v1.ResourceName(v1.ResourceHugePagesPrefix + "2Mi")
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("restored-pod")}, Spec: v1.PodSpec{
		NUMANode:   &node,
		Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceMemory: resource.MustParse("1Ki"), hugepages: resource.MustParse("2Mi")}}}},
	}}
	memoryState := state.NewMemoryState(logger)
	blocks := []state.Block{{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 1024}, {NUMAAffinity: []int{2}, Type: hugepages, Size: 2 * 1024 * 1024}}
	memoryState.SetMemoryBlocks(string(pod.UID), "app", blocks)
	m := &manager{state: memoryState}
	container := &pod.Spec.Containers[0]
	if err := m.CheckNUMAPlacement(pod, container); err != nil {
		t.Fatalf("matching restored assignment was refused: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err != nil {
		t.Fatalf("matching restored assignment could not reach runtime configuration: %v", err)
	}
	if actual := memoryState.GetMemoryBlocks(string(pod.UID), container.Name); len(actual) != 2 || actual[0].NUMAAffinity[0] != 2 || actual[1].NUMAAffinity[0] != 2 {
		t.Fatalf("restored memory assignment was modified: %+v", actual)
	}
	memoryState.SetMemoryBlocks(string(pod.UID), container.Name, []state.Block{{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 1024}, {NUMAAffinity: []int{1}, Type: hugepages, Size: 2 * 1024 * 1024}})
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), "NUMA node 2") {
		t.Fatalf("conflicting restored assignment was accepted: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err == nil {
		t.Fatal("conflicting restored assignment could reach runtime configuration")
	}
	memoryState.SetMemoryBlocks(string(pod.UID), container.Name, blocks[:1])
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), string(hugepages)) {
		t.Fatalf("missing restored hugepage assignment was accepted: %v", err)
	}
}

func TestNUMAPlacementMemoryCheckpointRecovery(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	node := int32(2)
	restart := v1.ContainerRestartPolicyAlways
	hugepages := v1.ResourceName(v1.ResourceHugePagesPrefix + "2Mi")
	makeContainer := func(name string) v1.Container {
		return v1.Container{Name: name, Resources: v1.ResourceRequirements{Requests: v1.ResourceList{
			v1.ResourceMemory: resource.MustParse("1Gi"), hugepages: resource.MustParse("2Mi"),
		}}}
	}
	sidecar := makeContainer("sidecar")
	sidecar.RestartPolicy = &restart
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("memory-checkpoint-pod")}, Spec: v1.PodSpec{
		NUMANode: &node, InitContainers: []v1.Container{makeContainer("init"), sidecar}, Containers: []v1.Container{makeContainer("app")},
	}}
	directory := t.TempDir()
	checkpoint, err := state.NewCheckpointState(logger, directory, "numa-memory", "Static")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.SetMachineState(state.NUMANodeMap{2: {MemoryMap: map[v1.ResourceName]*state.MemoryTable{
		v1.ResourceMemory: {TotalMemSize: 3 * 1024 * 1024 * 1024, Allocatable: 3 * 1024 * 1024 * 1024, Reserved: 3 * 1024 * 1024 * 1024},
		hugepages:         {TotalMemSize: 6 * 1024 * 1024, Allocatable: 6 * 1024 * 1024, Reserved: 6 * 1024 * 1024},
	}, NumberOfAssignments: 6}})
	blocks := []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 1024 * 1024 * 1024},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: 2 * 1024 * 1024},
	}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		checkpoint.SetMemoryBlocks(string(pod.UID), container.Name, blocks)
	}
	restored, err := state.NewCheckpointState(logger, directory, "numa-memory", "Static")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPolicyStatic(logger, nil, map[int]map[v1.ResourceName]uint64{0: {v1.ResourceMemory: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &manager{state: restored, policy: policy, sourcesReady: &sourcesReadyStub{}, activePods: func() []*v1.Pod { return []*v1.Pod{pod} }, containerMap: containermap.NewContainerMap()}
	if !m.HasRestoredNUMAPlacement(pod) {
		t.Fatal("restored memory checkpoint was not identified")
	}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if err := m.CheckNUMAPlacement(pod, &container); err != nil {
			t.Fatalf("%s checkpoint admission: %v", container.Name, err)
		}
		before := restored.GetMemoryBlocks(string(pod.UID), container.Name)
		if err := m.Allocate(ctx, pod, &container, lifecycle.AddOperation); err != nil {
			t.Fatalf("%s checkpoint reuse: %v", container.Name, err)
		}
		if after := restored.GetMemoryBlocks(string(pod.UID), container.Name); len(after) != len(before) || after[0].Size != before[0].Size || after[1].Size != before[1].Size {
			t.Fatalf("%s checkpoint memory moved: before=%+v after=%+v", container.Name, before, after)
		}
		if err := m.ValidateNUMAPlacement(pod, &container); err != nil {
			t.Fatalf("%s checkpoint restart: %v", container.Name, err)
		}
	}
	if nodeState := restored.GetMachineState()[2]; nodeState.MemoryMap[v1.ResourceMemory].Reserved != 3*1024*1024*1024 || nodeState.MemoryMap[hugepages].Reserved != 6*1024*1024 || nodeState.NumberOfAssignments != 6 {
		t.Fatalf("recovery reserved memory twice: %+v", nodeState)
	}
	restored.SetMemoryBlocks(string(pod.UID), "init", []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 0},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: 0},
	})
	if err := m.ValidateNUMAPlacement(pod, &pod.Spec.InitContainers[0]); err != nil {
		t.Fatalf("reused sequential init memory was refused: %v", err)
	}
	restored.SetMemoryBlocks(string(pod.UID), "init", blocks)
	restored.SetMemoryBlocks(string(pod.UID), "app", []state.Block{{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: blocks[0].Size / 2}, blocks[1]})
	if err := m.CheckNUMAPlacement(pod, &pod.Spec.Containers[0]); err == nil {
		t.Fatal("wrong-sized restored ordinary memory was accepted")
	}
	restored.SetMemoryBlocks(string(pod.UID), "app", blocks)
	restored.SetMemoryBlocks(string(pod.UID), "sidecar", []state.Block{blocks[0], {NUMAAffinity: []int{1}, Type: hugepages, Size: 2 * 1024 * 1024}})
	if err := m.CheckNUMAPlacement(pod, &pod.Spec.InitContainers[1]); err == nil || !strings.Contains(err.Error(), string(hugepages)) {
		t.Fatalf("conflicting sidecar hugepages were accepted: %v", err)
	}
	if err := m.Allocate(ctx, pod, &pod.Spec.InitContainers[1], lifecycle.AddOperation); err == nil {
		t.Fatal("conflicting sidecar hugepages were reallocated")
	}
	if err := m.ValidateNUMAPlacement(pod, &pod.Spec.InitContainers[1]); err == nil {
		t.Fatal("conflicting sidecar checkpoint could start")
	}
	if assigned := restored.GetMemoryBlocks(string(pod.UID), "sidecar"); len(assigned) != 2 || assigned[1].NUMAAffinity[0] != 1 {
		t.Fatalf("conflicting sidecar checkpoint was repaired: %+v", assigned)
	}
	for _, container := range []v1.Container{pod.Spec.InitContainers[0], pod.Spec.Containers[0]} {
		if err := m.ValidateNUMAPlacement(pod, &container); err != nil {
			t.Fatalf("valid %s checkpoint changed after conflict: %v", container.Name, err)
		}
	}
}

func TestNUMAPlacementRollbackRestoresInitReuse(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(2)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("rollback-pod")}, Spec: v1.PodSpec{
		NUMANode: &node, InitContainers: []v1.Container{{Name: "init"}}, Containers: []v1.Container{{Name: "app"}},
	}}
	const gib = uint64(1024 * 1024 * 1024)
	directory := t.TempDir()
	checkpoint, err := state.NewCheckpointState(logger, directory, "numa-rollback", "Static")
	if err != nil {
		t.Fatal(err)
	}
	hugepages := v1.ResourceName(v1.ResourceHugePagesPrefix + "1Gi")
	checkpoint.SetMachineState(state.NUMANodeMap{2: {MemoryMap: map[v1.ResourceName]*state.MemoryTable{
		v1.ResourceMemory: {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: gib, Reserved: gib},
		hugepages:         {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: gib, Reserved: gib},
	}, NumberOfAssignments: 2}})
	checkpoint.SetMemoryBlocks(string(pod.UID), "init", []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: gib},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: gib},
	})
	memoryState, err := state.NewCheckpointState(logger, directory, "numa-rollback", "Static")
	if err != nil {
		t.Fatal(err)
	}
	policy := &staticPolicy{initContainersReusableMemory: map[string]map[string]map[v1.ResourceName]uint64{string(pod.UID): {"2": {v1.ResourceMemory: gib, hugepages: gib}}}}
	m := &manager{state: memoryState, policy: policy, numaPlacementAllocations: map[string]bool{}}
	if !m.HasRestoredNUMAPlacement(pod) {
		t.Fatal("restored init assignment was not identified")
	}
	for attempt := 0; attempt < 2; attempt++ {
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
		if m.numaPlacementAllocations[numaPlacementKey(pod, &pod.Spec.InitContainers[0])] {
			t.Fatal("restored init allocation became newly acquired")
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
	}
	policy.RemoveContainer(logger, memoryState, string(pod.UID), "init")
	if blocks := memoryState.GetMemoryBlocks(string(pod.UID), "init"); blocks != nil {
		t.Fatalf("normal cleanup retained init blocks: %+v", blocks)
	}
	if nodeState := memoryState.GetMachineState()[2]; nodeState.MemoryMap[v1.ResourceMemory].Free != 2*gib || nodeState.MemoryMap[hugepages].Free != 2*gib || nodeState.NumberOfAssignments != 0 {
		t.Fatalf("normal cleanup did not release ordinary memory and hugepages: %+v", nodeState)
	}
}

func TestNUMAPlacementRecoveredHugepageReuseRollback(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	node := int32(2)
	const gib = uint64(1024 * 1024 * 1024)
	hugepages := v1.ResourceName(v1.ResourceHugePagesPrefix + "1Gi")
	resources := v1.ResourceRequirements{
		Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi"), hugepages: resource.MustParse("1Gi")},
		Limits:   v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi"), hugepages: resource.MustParse("1Gi")},
	}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("recovered-reuse-pod")}, Spec: v1.PodSpec{
		NUMANode: &node, InitContainers: []v1.Container{{Name: "init", Resources: resources}}, Containers: []v1.Container{{Name: "app", Resources: resources}},
	}}
	directory := t.TempDir()
	checkpoint, err := state.NewCheckpointState(logger, directory, "numa-hugepage-recovery", "Static")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.SetMachineState(state.NUMANodeMap{2: {Cells: []int{2}, MemoryMap: map[v1.ResourceName]*state.MemoryTable{
		v1.ResourceMemory: {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: gib, Reserved: gib},
		hugepages:         {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: gib, Reserved: gib},
	}, NumberOfAssignments: 2}})
	checkpoint.SetMemoryBlocks(string(pod.UID), "init", []state.Block{
		{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: gib},
		{NUMAAffinity: []int{2}, Type: hugepages, Size: gib},
	})
	restored, err := state.NewCheckpointState(logger, directory, "numa-hugepage-recovery", "Static")
	if err != nil {
		t.Fatal(err)
	}
	mask, err := bitmask.NewBitMask(2)
	if err != nil {
		t.Fatal(err)
	}
	affinity := topologymanager.NewFakeManagerWithHint(logger, &topologymanager.TopologyHint{NUMANodeAffinity: mask, Preferred: true})
	policy, err := NewPolicyStatic(logger, nil, map[int]map[v1.ResourceName]uint64{0: {v1.ResourceMemory: 1}}, affinity)
	if err != nil {
		t.Fatal(err)
	}
	m := &manager{state: restored, policy: policy, sourcesReady: &sourcesReadyStub{}, activePods: func() []*v1.Pod { return []*v1.Pod{pod} }, containerMap: containermap.NewContainerMap()}
	if err := m.Allocate(ctx, pod, &pod.Spec.InitContainers[0], lifecycle.AddOperation); err != nil {
		t.Fatalf("restored init allocation: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		rollback := m.SnapshotNUMAPlacement(pod, &pod.Spec.Containers[0])
		if err := m.Allocate(ctx, pod, &pod.Spec.Containers[0], lifecycle.AddOperation); err != nil {
			t.Fatalf("application allocation after recovery attempt %d: %v", attempt, err)
		}
		if err := m.ValidateNUMAPlacement(pod, &pod.Spec.Containers[0]); err != nil {
			t.Fatalf("application placement after recovery attempt %d: %v", attempt, err)
		}
		if err := rollback(logger); err != nil {
			t.Fatalf("rollback after recovery attempt %d: %v", attempt, err)
		}
		if blocks := restored.GetMemoryBlocks(string(pod.UID), "app"); blocks != nil {
			t.Fatalf("attempt %d retained new application memory: %+v", attempt, blocks)
		}
		if blocks := restored.GetMemoryBlocks(string(pod.UID), "init"); len(blocks) != 2 || blocks[0].Size != gib || blocks[1].Size != gib {
			t.Fatalf("attempt %d changed restored init memory: %+v", attempt, blocks)
		}
		nodeState := restored.GetMachineState()[2]
		if nodeState.MemoryMap[v1.ResourceMemory].Reserved != gib || nodeState.MemoryMap[hugepages].Reserved != gib || nodeState.NumberOfAssignments != 2 {
			t.Fatalf("attempt %d changed restored accounting: %+v", attempt, nodeState)
		}
	}
}
