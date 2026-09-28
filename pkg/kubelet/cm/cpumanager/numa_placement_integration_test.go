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

package cpumanager

import (
	"context"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	cpustate "k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager"
	memorystate "k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/test/utils/ktesting"
	"k8s.io/utils/cpuset"
)

type placementCPUProvider struct {
	policy Policy
	state  cpustate.State
}

func (p *placementCPUProvider) GetPodTopologyHints(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) map[string][]topologymanager.TopologyHint {
	return p.policy.GetPodTopologyHints(logger, p.state, pod, operation)
}

func (p *placementCPUProvider) GetTopologyHints(logger klog.Logger, pod *v1.Pod, container *v1.Container, operation lifecycle.Operation) map[string][]topologymanager.TopologyHint {
	return p.policy.GetTopologyHints(logger, p.state, pod, container, operation)
}

func (p *placementCPUProvider) Allocate(ctx context.Context, pod *v1.Pod, container *v1.Container, operation lifecycle.Operation) error {
	return p.policy.Allocate(klog.FromContext(ctx), p.state, pod, container, operation)
}

func (p *placementCPUProvider) AllocatePod(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) error {
	return p.policy.AllocatePod(logger, p.state, pod, operation)
}

func (p *placementCPUProvider) ReleaseNUMAPlacement(logger klog.Logger, pod *v1.Pod, container *v1.Container) error {
	return p.policy.RemoveContainer(logger, p.state, string(pod.UID), container.Name)
}

func (p *placementCPUProvider) NUMAPlacementAllocated(*v1.Pod, *v1.Container) bool { return false }

type placementMemoryProvider struct {
	policy        memorymanager.Policy
	state         memorystate.State
	injectFailure bool
}

func (p *placementMemoryProvider) GetPodTopologyHints(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) map[string][]topologymanager.TopologyHint {
	return p.policy.GetPodTopologyHints(logger, p.state, pod, operation)
}

func (p *placementMemoryProvider) GetTopologyHints(logger klog.Logger, pod *v1.Pod, container *v1.Container, operation lifecycle.Operation) map[string][]topologymanager.TopologyHint {
	return p.policy.GetTopologyHints(logger, p.state, pod, container, operation)
}

func (p *placementMemoryProvider) Allocate(ctx context.Context, pod *v1.Pod, container *v1.Container, operation lifecycle.Operation) error {
	if p.injectFailure {
		machine := p.state.GetMachineState()
		machine[int(*pod.Spec.NUMANode)].MemoryMap[v1.ResourceMemory].Free = 0
		p.state.SetMachineState(machine)
	}
	return p.policy.Allocate(ctx, p.state, pod, container, operation)
}

func (p *placementMemoryProvider) AllocatePod(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) error {
	return p.policy.AllocatePod(logger, p.state, pod, operation)
}

func TestNUMAPlacementRollsBackConcreteCPUAssignmentOnMemoryFailure(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	scope := topologymanager.NewPodScope(topologymanager.NewSingleNumaNodePolicy(&topologymanager.NUMAInfo{Nodes: []int{0, 1}}, topologymanager.PolicyOptions{}))
	cpuPolicy, err := NewStaticPolicy(logger, topoDualSocketHT, 1, cpuset.New(), scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	initialCPUs := cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)
	cpuState := &mockState{assignments: cpustate.ContainerCPUAssignments{}, defaultCPUSet: initialCPUs, baselines: cpustate.ContainerCPUBaselines{}}
	cpuProvider := &placementCPUProvider{policy: cpuPolicy, state: cpuState}
	memoryPolicy, err := memorymanager.NewPolicyStatic(logger, nil, map[int]map[v1.ResourceName]uint64{0: {v1.ResourceMemory: 512 * 1024 * 1024}}, scope)
	if err != nil {
		t.Fatal(err)
	}
	memoryState := memorystate.NewMemoryState(logger)
	const gib = uint64(1024 * 1024 * 1024)
	memoryState.SetMachineState(memorystate.NUMANodeMap{
		0: {MemoryMap: map[v1.ResourceName]*memorystate.MemoryTable{v1.ResourceMemory: {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: 2 * gib}}},
		1: {MemoryMap: map[v1.ResourceName]*memorystate.MemoryTable{v1.ResourceMemory: {TotalMemSize: 2 * gib, Allocatable: 2 * gib, Free: 2 * gib}}},
	})
	memoryProvider := &placementMemoryProvider{policy: memoryPolicy, state: memoryState, injectFailure: true}
	scope.AddHintProvider(logger, cpuProvider)
	scope.AddHintProvider(logger, memoryProvider)
	pod := makePod("numa-pod", "app", "1000m", "1000m")
	node := int32(1)
	pod.Spec.NUMANode = &node
	result := scope.Admit(ctx, pod, lifecycle.AddOperation)
	if result.Admit || !strings.Contains(result.Message, "memory") || !strings.Contains(result.Message, "NUMA node 1") {
		t.Fatalf("admission after injected memory shortage = %+v", result)
	}
	if _, exists := cpuState.GetCPUSet(string(pod.UID), "app"); exists || !cpuState.GetDefaultCPUSet().Equals(initialCPUs) {
		t.Fatalf("CPU allocation leaked after memory failure: assignment=%v default=%s", exists, cpuState.GetDefaultCPUSet())
	}
}

func TestNUMAPlacementRestoredCPUAssignment(t *testing.T) {
	node := int32(1)
	pod := makePod("restored-pod", "app", "1000m", "1000m")
	pod.Spec.NUMANode = &node
	assigned := cpuset.New(1)
	state := &mockState{assignments: cpustate.ContainerCPUAssignments{string(pod.UID): {"app": assigned}}}
	m := &manager{state: state, topology: topoDualSocketHT}
	container := &pod.Spec.Containers[0]
	if err := m.CheckNUMAPlacement(pod, container); err != nil {
		t.Fatalf("matching restored assignment was refused: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err != nil {
		t.Fatalf("matching restored assignment could not reach runtime configuration: %v", err)
	}
	if actual, exists := state.GetCPUSet(string(pod.UID), container.Name); !exists || !actual.Equals(assigned) {
		t.Fatalf("restored CPUs were modified: %s", actual)
	}
	state.SetCPUSet(string(pod.UID), container.Name, cpuset.New(0))
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), "NUMA node 1") {
		t.Fatalf("conflicting restored assignment was accepted: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err == nil {
		t.Fatal("conflicting restored assignment could reach runtime configuration")
	}
	state.SetCPUSet(string(pod.UID), container.Name, cpuset.New(1, 3))
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), "assigned 2 CPUs") {
		t.Fatalf("wrong-sized restored CPU assignment was accepted: %v", err)
	}
}

func TestNUMAPlacementCPUCheckpointRecovery(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	node := int32(1)
	restart := v1.ContainerRestartPolicyAlways
	makeContainer := func(name string) v1.Container {
		return makePod("unused", name, "1000m", "1000m").Spec.Containers[0]
	}
	sidecar := makeContainer("sidecar")
	sidecar.RestartPolicy = &restart
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("cpu-checkpoint-pod")}, Spec: v1.PodSpec{
		NUMANode: &node, InitContainers: []v1.Container{makeContainer("init"), sidecar}, Containers: []v1.Container{makeContainer("app")},
	}}
	directory := t.TempDir()
	checkpoint, err := cpustate.NewCheckpointState(logger, directory, "numa-cpu", "static", containermap.NewContainerMap())
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.SetDefaultCPUSet(cpuset.New(0, 2, 4, 6, 7, 8, 9, 10, 11))
	for name, cpu := range map[string]int{"init": 1, "sidecar": 3, "app": 5} {
		checkpoint.SetCPUSet(string(pod.UID), name, cpuset.New(cpu))
	}
	restored, err := cpustate.NewCheckpointState(logger, directory, "numa-cpu", "static", containermap.NewContainerMap())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewStaticPolicy(logger, topoDualSocketHT, 1, cpuset.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &manager{state: restored, topology: topoDualSocketHT, policy: policy, sourcesReady: &sourcesReadyStub{}, activePods: func() []*v1.Pod { return []*v1.Pod{pod} }, containerMap: containermap.NewContainerMap()}
	if !m.HasRestoredNUMAPlacement(pod) {
		t.Fatal("restored checkpoint was not identified")
	}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if err := m.CheckNUMAPlacement(pod, &container); err != nil {
			t.Fatalf("%s checkpoint admission: %v", container.Name, err)
		}
		before, _ := restored.GetCPUSet(string(pod.UID), container.Name)
		if err := m.Allocate(ctx, pod, &container, lifecycle.AddOperation); err != nil {
			t.Fatalf("%s checkpoint reuse: %v", container.Name, err)
		}
		if after, ok := restored.GetCPUSet(string(pod.UID), container.Name); !ok || !after.Equals(before) {
			t.Fatalf("%s checkpoint CPUs moved: before=%s after=%s", container.Name, before, after)
		}
		if err := m.ValidateNUMAPlacement(pod, &container); err != nil {
			t.Fatalf("%s checkpoint restart: %v", container.Name, err)
		}
	}
	if !restored.GetDefaultCPUSet().Equals(cpuset.New(0, 2, 4, 6, 7, 8, 9, 10, 11)) {
		t.Fatalf("recovery reserved CPUs twice: default=%s", restored.GetDefaultCPUSet())
	}
	restored.SetCPUSet(string(pod.UID), "sidecar", cpuset.New(0))
	if err := m.CheckNUMAPlacement(pod, &pod.Spec.InitContainers[1]); err == nil || !strings.Contains(err.Error(), "NUMA node 1") {
		t.Fatalf("conflicting sidecar checkpoint was accepted: %v", err)
	}
	if err := m.Allocate(ctx, pod, &pod.Spec.InitContainers[1], lifecycle.AddOperation); err == nil {
		t.Fatal("conflicting sidecar checkpoint was reallocated")
	}
	if err := m.ValidateNUMAPlacement(pod, &pod.Spec.InitContainers[1]); err == nil {
		t.Fatal("conflicting sidecar checkpoint could start")
	}
	if assigned, _ := restored.GetCPUSet(string(pod.UID), "sidecar"); !assigned.Equals(cpuset.New(0)) {
		t.Fatalf("conflicting sidecar checkpoint was repaired: %s", assigned)
	}
	for _, container := range []v1.Container{pod.Spec.InitContainers[0], pod.Spec.Containers[0]} {
		if err := m.ValidateNUMAPlacement(pod, &container); err != nil {
			t.Fatalf("valid %s checkpoint changed after conflict: %v", container.Name, err)
		}
	}
}

func TestNUMAPlacementPodWideAccounting(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	for _, tc := range []struct {
		name         string
		initCPUs     []string
		appCPUs      []string
		sidecar      bool
		omitted      bool
		memoryGiB    uint64
		wantCPUs     int
		wantMemoryGB uint64
		admit        bool
	}{
		{name: "concurrent applications", appCPUs: []string{"1", "1"}, wantCPUs: 2, wantMemoryGB: 2, admit: true},
		{name: "sequential init reuse", initCPUs: []string{"2", "2"}, appCPUs: []string{"1", "1"}, wantCPUs: 2, wantMemoryGB: 2, admit: true},
		{name: "sidecar overlaps later init and applications", initCPUs: []string{"2", "2"}, appCPUs: []string{"1", "1"}, sidecar: true, wantCPUs: 3, wantMemoryGB: 3, admit: true},
		{name: "omitted field retains pod-wide allocation and reuse", initCPUs: []string{"2", "2"}, appCPUs: []string{"1", "1"}, sidecar: true, omitted: true, wantCPUs: 3, wantMemoryGB: 3, admit: true},
		{name: "combined applications exceed requested node", appCPUs: []string{"4", "4"}},
		{name: "combined memory exceeds requested node", appCPUs: []string{"1", "1", "1"}, memoryGiB: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := topologymanager.NewPodScope(topologymanager.NewSingleNumaNodePolicy(&topologymanager.NUMAInfo{Nodes: []int{0, 1}}, topologymanager.PolicyOptions{}))
			cpuPolicy, err := NewStaticPolicy(logger, topoDualSocketHT, 1, cpuset.New(), scope, nil)
			if err != nil {
				t.Fatal(err)
			}
			initialCPUs := cpuset.New(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)
			cpuState := &mockState{assignments: cpustate.ContainerCPUAssignments{}, defaultCPUSet: initialCPUs, baselines: cpustate.ContainerCPUBaselines{}}
			memoryPolicy, err := memorymanager.NewPolicyStatic(logger, nil, map[int]map[v1.ResourceName]uint64{0: {v1.ResourceMemory: 512 * 1024 * 1024}}, scope)
			if err != nil {
				t.Fatal(err)
			}
			memoryState := memorystate.NewMemoryState(logger)
			const gib = uint64(1024 * 1024 * 1024)
			requestedNodeMemory := uint64(4) * gib
			if tc.memoryGiB != 0 {
				requestedNodeMemory = tc.memoryGiB * gib
			}
			memoryState.SetMachineState(memorystate.NUMANodeMap{
				0: {MemoryMap: map[v1.ResourceName]*memorystate.MemoryTable{v1.ResourceMemory: {TotalMemSize: 4 * gib, Allocatable: 4 * gib, Free: 4 * gib}}},
				1: {MemoryMap: map[v1.ResourceName]*memorystate.MemoryTable{v1.ResourceMemory: {TotalMemSize: requestedNodeMemory, Allocatable: requestedNodeMemory, Free: requestedNodeMemory}}},
			})
			scope.AddHintProvider(logger, &placementCPUProvider{policy: cpuPolicy, state: cpuState})
			scope.AddHintProvider(logger, &placementMemoryProvider{policy: memoryPolicy, state: memoryState})
			pod := makePod("pod-wide-"+tc.name, "app-0", tc.appCPUs[0], tc.appCPUs[0])
			node := int32(1)
			if !tc.omitted {
				pod.Spec.NUMANode = &node
			}
			for i, amount := range tc.appCPUs[1:] {
				container := makePod("unused", "app-"+string(rune('1'+i)), amount, amount).Spec.Containers[0]
				pod.Spec.Containers = append(pod.Spec.Containers, container)
			}
			for i, amount := range tc.initCPUs {
				container := makePod("unused", "init-"+string(rune('0'+i)), amount, amount).Spec.Containers[0]
				pod.Spec.InitContainers = append(pod.Spec.InitContainers, container)
			}
			if tc.sidecar {
				restart := v1.ContainerRestartPolicyAlways
				pod.Spec.InitContainers[0].RestartPolicy = &restart
				pod.Spec.InitContainers[0].Resources.Requests[v1.ResourceCPU] = pod.Spec.Containers[0].Resources.Requests[v1.ResourceCPU]
				pod.Spec.InitContainers[0].Resources.Limits[v1.ResourceCPU] = pod.Spec.Containers[0].Resources.Limits[v1.ResourceCPU]
			}
			result := scope.Admit(ctx, pod, lifecycle.AddOperation)
			if result.Admit != tc.admit {
				t.Fatalf("admission = %+v, want admit %t", result, tc.admit)
			}
			if !tc.admit {
				if !cpuState.GetDefaultCPUSet().Equals(initialCPUs) {
					t.Fatalf("CPU state changed after capacity refusal: %s", cpuState.GetDefaultCPUSet())
				}
				return
			}
			for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
				cpus, ok := cpuState.GetCPUSet(string(pod.UID), container.Name)
				if !ok || cpus.IsEmpty() || !tc.omitted && !cpus.IsSubsetOf(topoDualSocketHT.CPUDetails.CPUsInNUMANodes(1)) {
					t.Fatalf("%s CPUs = %s, want requested node", container.Name, cpus)
				}
				blocks := memoryState.GetMemoryBlocks(string(pod.UID), container.Name)
				if len(blocks) != 1 || len(blocks[0].NUMAAffinity) != 1 || !tc.omitted && blocks[0].NUMAAffinity[0] != 1 {
					t.Fatalf("%s memory blocks = %+v, want requested node", container.Name, blocks)
				}
			}
			if used := initialCPUs.Difference(cpuState.GetDefaultCPUSet()).Size(); used != tc.wantCPUs {
				t.Fatalf("reserved CPUs = %d, want %d", used, tc.wantCPUs)
			}
			if tc.omitted {
				machine := memoryState.GetMachineState()
				free := machine[0].MemoryMap[v1.ResourceMemory].Free + machine[1].MemoryMap[v1.ResourceMemory].Free
				want := 4*gib + requestedNodeMemory - tc.wantMemoryGB*1000*1000*1000
				if free != want {
					t.Fatalf("free memory across nodes = %d, want %d", free, want)
				}
			} else {
				if free := memoryState.GetMachineState()[1].MemoryMap[v1.ResourceMemory].Free; free != requestedNodeMemory-tc.wantMemoryGB*1000*1000*1000 {
					t.Fatalf("free memory = %d, want %d", free, requestedNodeMemory-tc.wantMemoryGB*1000*1000*1000)
				}
			}
			for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
				if err := cpuPolicy.RemoveContainer(logger, cpuState, string(pod.UID), container.Name); err != nil {
					t.Fatal(err)
				}
				memoryPolicy.RemoveContainer(logger, memoryState, string(pod.UID), container.Name)
			}
			machine := memoryState.GetMachineState()
			if !cpuState.GetDefaultCPUSet().Equals(initialCPUs) || len(memoryState.GetMemoryAssignments()[string(pod.UID)]) != 0 || machine[0].MemoryMap[v1.ResourceMemory].Free != 4*gib || machine[1].MemoryMap[v1.ResourceMemory].Free != requestedNodeMemory {
				t.Fatalf("normal cleanup retained allocations: CPUs=%s memory=%v nodes=%+v", cpuState.GetDefaultCPUSet(), memoryState.GetMemoryAssignments()[string(pod.UID)], machine)
			}
		})
	}
}

func TestNUMAPlacementRollbackPreservesOtherPodCPUs(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(1)
	pod := makePod("rollback", "app", "1", "1")
	pod.Spec.NUMANode = &node
	pod.Spec.InitContainers = []v1.Container{{Name: "init"}}
	defaultCPUs := cpuset.New(0, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)
	state := &mockState{assignments: cpustate.ContainerCPUAssignments{string(pod.UID): {"init": cpuset.New(1)}}, defaultCPUSet: defaultCPUs, baselines: cpustate.ContainerCPUBaselines{}}
	policy, err := NewStaticPolicy(logger, topoDualSocketHT, 1, cpuset.New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &manager{state: state, policy: policy, topology: topoDualSocketHT, numaPlacementAllocations: map[string]bool{}}
	rollback := m.SnapshotNUMAPlacement(pod, &pod.Spec.Containers[0])
	state.SetCPUSet(string(pod.UID), "app", cpuset.New(5))
	state.SetCPUSet("other-pod", "app", cpuset.New(3))
	state.SetDefaultCPUSet(defaultCPUs.Difference(cpuset.New(3, 5)))
	m.numaPlacementAllocations[numaPlacementKey(pod, &pod.Spec.Containers[0])] = true
	if err := rollback(logger); err != nil {
		t.Fatal(err)
	}
	if _, exists := state.GetCPUSet(string(pod.UID), "app"); exists {
		t.Fatal("new application assignment remained committed")
	}
	if restored, exists := state.GetCPUSet(string(pod.UID), "init"); !exists || !restored.Equals(cpuset.New(1)) || m.numaPlacementAllocations[numaPlacementKey(pod, &pod.Spec.InitContainers[0])] {
		t.Fatalf("restored init assignment changed: %s", restored)
	}
	if actual, exists := state.GetCPUSet("other-pod", "app"); !exists || !actual.Equals(cpuset.New(3)) {
		t.Fatalf("other pod assignment changed: %s", actual)
	}
	if !state.GetDefaultCPUSet().Equals(defaultCPUs.Difference(cpuset.New(3))) {
		t.Fatalf("default CPU set = %s, want other pod CPU retained", state.GetDefaultCPUSet())
	}
}
