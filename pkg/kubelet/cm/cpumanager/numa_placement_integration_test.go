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
	"k8s.io/klog/v2"
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

func TestNUMAPlacementRefusesRestoredCPUAssignment(t *testing.T) {
	node := int32(1)
	pod := makePod("restored-pod", "app", "1000m", "1000m")
	pod.Spec.NUMANode = &node
	assigned := cpuset.New(1)
	state := &mockState{assignments: cpustate.ContainerCPUAssignments{string(pod.UID): {"app": assigned}}}
	m := &manager{state: state, topology: topoDualSocketHT}
	container := &pod.Spec.Containers[0]
	if err := m.CheckNUMAPlacement(pod, container); err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("restored assignment was not refused: %v", err)
	}
	if err := m.ValidateNUMAPlacement(pod, container); err == nil {
		t.Fatal("restored assignment could reach runtime configuration")
	}
	if actual, exists := state.GetCPUSet(string(pod.UID), container.Name); !exists || !actual.Equals(assigned) {
		t.Fatalf("restored CPUs were modified: %s", actual)
	}
}
