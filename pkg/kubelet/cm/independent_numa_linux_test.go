//go:build linux

/*
Copyright The Kubernetes Authors.

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

package cm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	cpustate "k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager"
	memorystate "k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
)

type numaTestSourcesNotReady struct{}

func (numaTestSourcesNotReady) AddSource(string) {}
func (numaTestSourcesNotReady) AllReady() bool   { return false }

// Fail after the real managers have allocated, exercising the admission transaction.
type numaAllocationFailure struct{ topologymanager.HintProvider }

func (numaAllocationFailure) Allocate(context.Context, *v1.Pod, *v1.Container, lifecycle.Operation) error {
	return errors.New("injected provider failure")
}

func splitNUMAAdmissions(t *testing.T) float64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "kubelet_topology_manager_split_numa_admissions_total" {
			return family.GetMetric()[0].GetCounter().GetValue()
		}
	}
	return 0
}

func TestIndependentNUMAAllocationAndRuntimeMasks(t *testing.T) {
	metrics.Register()
	const gib = uint64(1024 * 1024 * 1024)
	info := &cadvisorapi.MachineInfo{NumCores: 12, Topology: []cadvisorapi.Node{{Id: 0, Memory: 8 * gib}, {Id: 2, Memory: 4 * gib}, {Id: 3, Memory: 2 * gib}}}
	for cpu := 0; cpu < 12; cpu++ {
		index := 0
		if cpu >= 8 {
			index = 1
		}
		info.Topology[index].Cores = append(info.Topology[index].Cores, cadvisorapi.Core{Id: cpu, SocketID: index, Threads: []int{cpu}})
	}
	for _, policyName := range []string{topologymanager.PolicySingleNumaNode, topologymanager.PolicyIndependentNUMA} {
		t.Run(policyName, func(t *testing.T) {
			for _, tc := range []struct {
				name                    string
				legacy                  bool
				cpu, memory             string
				wantFailure             string
				injectFailure           bool
				memoryPolicy            string
				cpuTarget, memoryTarget *int32
				dropCheckpoint          string
				distances               map[int][]uint64
				wantCPU, wantMemory     string
				memoryCapacity          map[int]uint64
				changeDistances         bool
				preferClosest           bool
				conflictingCPU          bool
				conflictingMemory       bool
				failPartialRecovery     bool
			}{
				{name: "CPU-only common node", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](0)},
				{name: "CPU-only CPU-less nearest directed", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{0: {10, 11, 20}, 2: {40, 10, 20}, 3: {20, 60, 10}}},
				{name: "CPU-only common node with optional closest flag", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "2", preferClosest: true},
				{name: "CPU-only sole feasible memory without distances", cpu: "1", memory: "5Gi", cpuTarget: ptr.To[int32](2)},
				{name: "CPU-only cannot aggregate memory", cpu: "1", memory: "8Gi", cpuTarget: ptr.To[int32](2), wantFailure: "no feasible singleton memory"},
				{name: "CPU-only CPU shortage", cpu: "5", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantFailure: "CPUs"},
				{name: "CPU-only equal distances ascending ID", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {20, 10, 20}}},
				{name: "CPU-only nearer memory lacks capacity", cpu: "1", memory: "3Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 11}}},
				{name: "CPU-only sole CPU-less memory candidate", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{0: gib, 2: gib}},
				{name: "CPU-only missing CPU distance row", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{0: {10, 11, 20}, 3: {20, 12, 10}}, wantFailure: "distance"},
				{name: "CPU-only truncated row", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {20, 10}}, wantFailure: "distance"},
				{name: "CPU-only padded raw-ID row", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {20, 0, 10, 30}}, wantFailure: "distance"},
				{name: "CPU-only unknown candidate cannot be discarded", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {20, 10, 0}}, wantFailure: "distance"},
				{name: "CPU-only unreachable candidate", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {255, 10, 30}}, wantFailure: "distance"},
				{name: "CPU-only invalid local distance", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {20, 20, 30}}, wantFailure: "distance"},
				{name: "CPU-only restored memory outranks new locality", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, changeDistances: true},
				{name: "CPU-only partial CPU restoration", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, dropCheckpoint: "cpu_manager_state", changeDistances: true},
				{name: "CPU-only partial memory restoration", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, dropCheckpoint: "memory_manager_state"},
				{name: "CPU-only rollback preserves surviving automatic memory", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, dropCheckpoint: "cpu_manager_state", failPartialRecovery: true},
				{name: "CPU-only rollback preserves surviving CPUs", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, dropCheckpoint: "memory_manager_state", failPartialRecovery: true},
				{name: "CPU-only failure after automatic memory allocation", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, injectFailure: true, wantFailure: "injected provider failure"},
				{name: "CPU-only conflicting multi-node memory checkpoint", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), wantMemory: "3", memoryCapacity: map[int]uint64{2: gib}, distances: map[int][]uint64{2: {40, 10, 20}}, conflictingMemory: true},

				{name: "memory-only common node", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](0)},
				{name: "memory-only common node with optional closest flag", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](0), preferClosest: true},
				{name: "memory-only common node infeasible selects sole candidate", cpu: "5", memory: "1Gi", memoryTarget: ptr.To[int32](2), wantCPU: "1-5"},
				{name: "memory-only conflicting multi-node CPU checkpoint", cpu: "2", memory: "1Gi", memoryTarget: ptr.To[int32](0), wantCPU: "1-2", conflictingCPU: true},
				{name: "memory-only memory shortage", cpu: "1", memory: "3Gi", memoryTarget: ptr.To[int32](3), wantFailure: "ordinary memory"},
				{name: "memory-only equal distances ascending ID", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{0: {10, 20, 30}, 2: {20, 10, 30}}},
				{name: "memory-only sole feasible candidate without distances", cpu: "5", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantCPU: "1-5"},
				{name: "memory-only nearer candidate lacks capacity", cpu: "5", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantCPU: "1-5", distances: map[int][]uint64{0: {10, 20, 40}, 2: {20, 10, 11}}},
				{name: "memory-only cannot aggregate CPUs", cpu: "8", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantFailure: "no feasible singleton CPU"},
				{name: "memory-only missing all distances", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantFailure: "distance"},
				{name: "memory-only unknown candidate cannot be discarded", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{2: {20, 10, 30}}, wantFailure: "distance"},
				{name: "memory-only truncated row", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{0: {10, 20}, 2: {20, 10, 30}}, wantFailure: "distance"},
				{name: "memory-only padded raw-ID row ambiguous", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{0: {10, 0, 20, 30}, 2: {20, 0, 10, 30}}, wantFailure: "distance"},
				{name: "memory-only zero distance", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{0: {10, 20, 0}, 2: {20, 10, 30}}, wantFailure: "distance"},
				{name: "memory-only unreachable distance", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{0: {10, 20, 255}, 2: {20, 10, 30}}, wantFailure: "distance"},
				{name: "memory-only invalid local distance", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), distances: map[int][]uint64{0: {20, 20, 30}, 2: {20, 10, 30}}, wantFailure: "distance"},
				{name: "memory-only restored target outranks new locality", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantCPU: "8", distances: map[int][]uint64{0: {10, 20, 40}, 2: {20, 10, 30}}, changeDistances: true},
				{name: "memory-only partial CPU restoration", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantCPU: "8", distances: map[int][]uint64{0: {10, 20, 40}, 2: {20, 10, 30}}, dropCheckpoint: "cpu_manager_state"},
				{name: "memory-only partial memory restoration", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantCPU: "8", distances: map[int][]uint64{0: {10, 20, 40}, 2: {20, 10, 30}}, dropCheckpoint: "memory_manager_state"},
				{name: "memory-only failure after allocation", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](0), injectFailure: true, wantFailure: "injected provider failure"},

				{name: "memory-only CPU-less nearest directed", cpu: "1", memory: "1Gi", memoryTarget: ptr.To[int32](3), wantCPU: "8", distances: map[int][]uint64{0: {10, 20, 40}, 2: {20, 10, 30}, 3: {11, 60, 10}}},
				{name: "ordinary", cpu: "1", memory: "1Gi"},
				{name: "ordinary best effort"},
				{name: "ordinary fractional CPU", cpu: "500m", memory: "1Gi"},
				{name: "ordinary without Memory Manager", cpu: "1", memory: "1Gi", memoryPolicy: "None"},
				{name: "legacy", legacy: true, cpu: "1", memory: "1Gi"},
				{name: "legacy CPU shortage with spare CPUs elsewhere", legacy: true, cpu: "5", memory: "1Gi", wantFailure: "CPUs"},
				{name: "legacy memory shortage with spare memory elsewhere", legacy: true, cpu: "1", memory: "4Gi", wantFailure: "memory"},
				{name: "legacy provider failure after allocation", legacy: true, cpu: "1", memory: "1Gi", wantFailure: "injected provider failure", injectFailure: true},
				{name: "ordinary cannot span nodes", cpu: "8", memory: "1Gi", wantFailure: "Topology"},
				{name: "explicit split targets", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryTarget: ptr.To[int32](0)},
				{name: "explicit memory without CPUs", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](0), memoryTarget: ptr.To[int32](3)},
				{name: "explicit equal targets", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryTarget: ptr.To[int32](2)},
				{name: "explicit partial CPU restoration", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryTarget: ptr.To[int32](3), dropCheckpoint: "cpu_manager_state"},
				{name: "explicit partial memory restoration", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryTarget: ptr.To[int32](3), dropCheckpoint: "memory_manager_state"},
				{name: "explicit CPU shortage", cpu: "5", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryTarget: ptr.To[int32](0), wantFailure: "CPUs"},
				{name: "explicit memory shortage", cpu: "1", memory: "3Gi", cpuTarget: ptr.To[int32](0), memoryTarget: ptr.To[int32](3), wantFailure: "memory"},
				{name: "explicit failure after allocation", cpu: "1", memory: "1Gi", cpuTarget: ptr.To[int32](2), memoryTarget: ptr.To[int32](3), injectFailure: true, wantFailure: "injected provider failure"},
			} {
				if (tc.cpuTarget != nil || tc.memoryTarget != nil) && policyName != topologymanager.PolicyIndependentNUMA {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					logger, ctx := ktesting.NewTestContext(t)
					var resources v1.ResourceList
					if tc.cpu != "" {
						resources = v1.ResourceList{v1.ResourceCPU: resource.MustParse(tc.cpu), v1.ResourceMemory: resource.MustParse(tc.memory)}
					}
					pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "allocation-masks"}, Spec: v1.PodSpec{Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: resources, Limits: resources}}}}}
					pod.Spec.CPUNUMA, pod.Spec.MemNUMA = tc.cpuTarget, tc.memoryTarget
					if tc.legacy {
						node := int32(2)
						pod.Spec.NUMANode = &node
					}
					// Shuffled discovery order must not change the kernel's ascending-ID columns.
					machine := *info
					machine.Topology = []cadvisorapi.Node{info.Topology[2], info.Topology[0], info.Topology[1]}
					for i := range machine.Topology {
						machine.Topology[i].Distances = tc.distances[machine.Topology[i].Id]
						if capacity, ok := tc.memoryCapacity[machine.Topology[i].Id]; ok {
							machine.Topology[i].Memory = capacity
						}
					}
					info := &machine
					directory := t.TempDir()
					var store topologymanager.Store
					var addTrackedContainer func(*v1.Pod, *v1.Container, string)
					var removeTrackedContainer func(klog.Logger, string) error

					setup := func() (func(*v1.Pod) lifecycle.PodAdmitResult, cpumanager.Manager, memorymanager.Manager) {
						t.Helper()
						numaInfo := &topologymanager.NUMAInfo{Nodes: []int{3, 0, 2}, NUMADistances: tc.distances}
						policy := topologymanager.NewSingleNumaNodePolicy(numaInfo, topologymanager.PolicyOptions{})
						if policyName == topologymanager.PolicyIndependentNUMA {
							policy = topologymanager.NewIndependentNUMAPolicy(numaInfo, topologymanager.PolicyOptions{PreferClosestNUMA: tc.preferClosest})
						}
						scope := topologymanager.NewPodScope(policy)
						store = scope
						addTrackedContainer, removeTrackedContainer = scope.AddContainer, scope.RemoveContainer
						admit := func(pod *v1.Pod) lifecycle.PodAdmitResult { return scope.Admit(ctx, pod, lifecycle.AddOperation) }
						addProvider := scope.AddHintProvider
						// Explicit-file tests on macOS exercise portable allocation and Linux
						// mask construction; normal Linux tests also cover manager selection.
						if runtime.GOOS == "linux" {
							options := map[string]string{}
							if tc.preferClosest {
								options[topologymanager.PreferClosestNUMANodes] = "true"
							}
							manager, err := topologymanager.NewManager(logger, info.Topology, policyName, "pod", options)
							if err != nil {
								t.Fatal(err)
							}
							store = manager
							addTrackedContainer = func(p *v1.Pod, c *v1.Container, id string) { manager.AddContainer(logger, p, c, id) }
							removeTrackedContainer = manager.RemoveContainer
							addProvider = manager.AddHintProvider
							admit = func(pod *v1.Pod) lifecycle.PodAdmitResult {
								return manager.Admit(ctx, &lifecycle.PodAdmitAttributes{Pod: pod, Operation: lifecycle.AddOperation})
							}
						}
						reservations := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")}
						cpu, err := cpumanager.NewManager(logger, "static", map[string]string{"strict-cpu-reservation": "true"}, time.Hour, info, cpuset.New(0), reservations, directory, store)
						if err != nil {
							t.Fatal(err)
						}
						memoryPolicy := tc.memoryPolicy
						if memoryPolicy == "" {
							memoryPolicy = "Static"
						}
						memory, err := memorymanager.NewManager(logger, memoryPolicy, info, reservations, []kubeletconfig.MemoryReservation{
							{NumaNode: 0, Limits: v1.ResourceList{v1.ResourceMemory: resource.MustParse("512Mi")}},
							{NumaNode: 2, Limits: v1.ResourceList{v1.ResourceMemory: resource.MustParse("512Mi")}},
						}, directory, store)
						if err != nil {
							t.Fatal(err)
						}
						// Sources are not ready, so these synthetic assignments survive without
						// a running kubelet or a container runtime reconciliation loop.
						if err := cpu.Start(ctx, func() []*v1.Pod { return nil }, numaTestSourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
							t.Fatal(err)
						}
						if err := memory.Start(ctx, func() []*v1.Pod { return nil }, numaTestSourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
							t.Fatal(err)
						}
						addProvider(logger, cpu)
						addProvider(logger, memory)
						if tc.injectFailure {
							addProvider(logger, numaAllocationFailure{HintProvider: memory})
						}
						return admit, cpu, memory
					}
					admit, cpu, memory := setup()
					beforeCPU := cpu.State().GetDefaultCPUSet()
					beforeMemory := memory.State().GetMachineState()
					expectedCPUNode := int32(0)
					if tc.cpuTarget != nil {
						expectedCPUNode = *tc.cpuTarget
					} else if tc.wantCPU == "8" {
						expectedCPUNode = 2
					}
					expectedMemoryNode := "0"
					if tc.memoryTarget != nil {
						expectedMemoryNode = fmt.Sprint(*tc.memoryTarget)
					} else if tc.wantMemory != "" {
						expectedMemoryNode = tc.wantMemory
					}
					split := (tc.cpuTarget != nil || tc.memoryTarget != nil) && fmt.Sprint(expectedCPUNode) != expectedMemoryNode
					var allocatedCPU string
					var allocatedMemory interface{}
					for attempt := 0; attempt < 3; attempt++ {
						splitBefore := splitNUMAAdmissions(t)
						counter := metrics.ContainerAlignedComputeResources.WithLabelValues(metrics.AlignScopePod, metrics.AlignedNUMANode)
						before, err := testutil.GetCounterMetricValue(counter)
						if err != nil {
							t.Fatal(err)
						}
						result := admit(pod)
						if tc.wantFailure != "" {
							if splitNUMAAdmissions(t) != splitBefore {
								t.Fatal("refusal counted as a split admission")
							}
							if result.Admit || !strings.Contains(result.Message, tc.wantFailure) {
								t.Fatalf("admission = %+v, want %s failure", result, tc.wantFailure)
							}
							if !cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(memory.State().GetMachineState(), beforeMemory) || len(cpu.State().GetCPUAssignments()) != 0 || len(memory.State().GetMemoryAssignments()) != 0 {
								t.Fatal("refusal leaked CPU or memory allocations")
							}
							if store.GetAffinity(logger, string(pod.UID), "app").NUMANodeAffinity != nil || topologymanager.GetMemoryAffinity(store, logger, string(pod.UID), "app").NUMANodeAffinity != nil {
								t.Fatal("refusal retained topology affinities")
							}
							continue
						}
						if !result.Admit {
							t.Fatalf("admission = %+v", result)
						}
						if attempt == 0 {
							allocatedCPU, allocatedMemory = cpu.State().GetDefaultCPUSet().String(), memory.State().GetMachineState()
						} else if cpu.State().GetDefaultCPUSet().String() != allocatedCPU || !reflect.DeepEqual(memory.State().GetMachineState(), allocatedMemory) {
							t.Fatal("recovery duplicated resource reservations")
						}
						after, err := testutil.GetCounterMetricValue(counter)
						if err != nil {
							t.Fatal(err)
						}
						wantAligned := float64(1)
						if policyName == topologymanager.PolicyIndependentNUMA && (tc.cpu == "" || tc.cpu == "500m" || tc.memoryPolicy == "None") {
							wantAligned = 0
						}
						if split {
							wantAligned = 0
						}
						if after-before != wantAligned {
							t.Fatalf("alignment metric increment = %v, want %v", after-before, wantAligned)
						}
						wantSplit := float64(0)
						if split {
							wantSplit = 1
						}
						if delta := splitNUMAAdmissions(t) - splitBefore; delta != wantSplit {
							t.Fatalf("split admission increment = %v, want %v", delta, wantSplit)
						}
						config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
						lifecycle := &internalContainerLifecycleImpl{cpuManager: cpu, memoryManager: memory}
						if err := lifecycle.PreCreateContainer(logger, pod, &pod.Spec.Containers[0], config); err != nil {
							t.Fatal(err)
						}
						wantCPU, wantMemory := "1", "0"
						if tc.cpu == "" || tc.cpu == "500m" {
							wantCPU = "1-11"
							if tc.cpu == "" {
								wantMemory = ""
							}
						}
						if tc.memoryPolicy == "None" {
							wantMemory = ""
						}
						if tc.legacy {
							wantCPU, wantMemory = "8", "2"
						}
						if tc.cpuTarget != nil || tc.memoryTarget != nil {
							wantCPU = "1"
							if tc.cpuTarget != nil && *tc.cpuTarget == 2 {
								wantCPU = "8"
							}
							if tc.wantCPU != "" {
								wantCPU = tc.wantCPU
							}
							wantMemory = expectedMemoryNode
						}
						if config.Linux.Resources.CpusetCpus != wantCPU || config.Linux.Resources.CpusetMems != wantMemory {
							t.Fatalf("runtime masks = %q/%q, want %q/%q", config.Linux.Resources.CpusetCpus, config.Linux.Resources.CpusetMems, wantCPU, wantMemory)
						}
						if tc.conflictingMemory {
							checkpoint, err := memorystate.NewCheckpointState(logger, directory, "memory_manager_state", "Static")
							if err != nil {
								t.Fatal(err)
							}
							blocks := checkpoint.GetMemoryBlocks(string(pod.UID), "app")
							blocks[0].NUMAAffinity = []int{0, 3}
							machine := checkpoint.GetMachineState()
							machine[3].Cells = []int{0, 3}
							machine[0].Cells = []int{0, 3}
							machine[3].MemoryMap[v1.ResourceMemory].Reserved -= blocks[0].Size
							machine[3].MemoryMap[v1.ResourceMemory].Free += blocks[0].Size
							machine[0].MemoryMap[v1.ResourceMemory].Reserved += blocks[0].Size
							machine[0].MemoryMap[v1.ResourceMemory].Free -= blocks[0].Size
							machine[0].NumberOfAssignments++
							checkpoint.SetMachineState(machine)
							checkpoint.SetMemoryBlocks(string(pod.UID), "app", blocks)
							admit, cpu, memory = setup()
							beforeCPU := cpu.State().GetDefaultCPUSet()
							beforeMemory := memory.State().GetMachineState()
							if result := admit(pod); result.Admit || !strings.Contains(result.Message, "conflict") {
								t.Fatalf("multi-node memory recovery = %+v", result)
							}
							if err := (&internalContainerLifecycleImpl{cpuManager: cpu, memoryManager: memory}).PreCreateContainer(logger, pod, &pod.Spec.Containers[0], config); err == nil {
								t.Fatal("multi-node memory checkpoint reached runtime")
							}
							if !reflect.DeepEqual(memory.State().GetMemoryBlocks(string(pod.UID), "app"), blocks) || !reflect.DeepEqual(memory.State().GetMachineState(), beforeMemory) || !cpu.State().GetDefaultCPUSet().Equals(beforeCPU) {
								t.Fatal("conflicting memory checkpoint was changed")
							}
							return
						}
						if tc.conflictingCPU {
							checkpoint, err := cpustate.NewCheckpointState(logger, directory, "cpu_manager_state", "static", containermap.NewContainerMap())
							if err != nil {
								t.Fatal(err)
							}
							assigned, _ := checkpoint.GetCPUSet(string(pod.UID), "app")
							invalid := cpuset.New(1, 8)
							checkpoint.SetDefaultCPUSet(checkpoint.GetDefaultCPUSet().Union(assigned).Difference(invalid))
							checkpoint.SetCPUSet(string(pod.UID), "app", invalid)
							admit, cpu, memory = setup()
							before := cpu.State().GetDefaultCPUSet()
							if result := admit(pod); result.Admit || !strings.Contains(result.Message, "conflict") {
								t.Fatalf("multi-node recovery = %+v", result)
							}
							if err := (&internalContainerLifecycleImpl{cpuManager: cpu, memoryManager: memory}).PreCreateContainer(logger, pod, &pod.Spec.Containers[0], config); err == nil {
								t.Fatal("multi-node checkpoint reached runtime")
							}
							actual, _ := cpu.State().GetCPUSet(string(pod.UID), "app")
							if !actual.Equals(invalid) || !before.Equals(cpu.State().GetDefaultCPUSet()) {
								t.Fatal("conflicting checkpoint was changed")
							}
							return
						}
						// Recreate both managers and topology affinity from their checkpoints.
						// A repeated admission must reuse concrete assignments and masks.
						if attempt == 0 && tc.dropCheckpoint != "" {
							if err := os.Remove(filepath.Join(directory, tc.dropCheckpoint)); err != nil {
								t.Fatal(err)
							}
						}
						if tc.changeDistances {
							if attempt == 0 {
								tc.distances = map[int][]uint64{0: {10, 20, 11}, 2: {20, 10, 50}}
								if tc.memoryTarget == nil {
									tc.distances = map[int][]uint64{2: {11, 10, 50}}
								}
							} else {
								tc.distances = nil
							}
							for i := range info.Topology {
								info.Topology[i].Distances = tc.distances[info.Topology[i].Id]
							}
						}
						if attempt == 0 && tc.failPartialRecovery {
							tc.injectFailure = true
							admit, cpu, memory = setup()
							beforeCPU := cpu.State().GetCPUAssignments()
							beforePool := cpu.State().GetDefaultCPUSet()
							beforeMemory := memory.State().GetMemoryAssignments()
							beforeAccounting := memory.State().GetMachineState()
							for retry := 0; retry < 3; retry++ {
								if result := admit(pod); result.Admit || !strings.Contains(result.Message, "injected provider failure") {
									t.Fatalf("partial recovery failure = %+v", result)
								}
								if !reflect.DeepEqual(cpu.State().GetCPUAssignments(), beforeCPU) || !cpu.State().GetDefaultCPUSet().Equals(beforePool) || !reflect.DeepEqual(memory.State().GetMemoryAssignments(), beforeMemory) || !reflect.DeepEqual(memory.State().GetMachineState(), beforeAccounting) {
									t.Fatal("partial recovery rollback changed surviving assignments or leaked resources")
								}
								if store.GetAffinity(logger, string(pod.UID), "app").NUMANodeAffinity != nil || topologymanager.GetMemoryAffinity(store, logger, string(pod.UID), "app").NUMANodeAffinity != nil {
									t.Fatal("partial recovery rollback retained new affinities")
								}
							}
							return
						}
						admit, cpu, memory = setup()
					}
					if (tc.legacy || tc.cpuTarget != nil || tc.memoryTarget != nil) && tc.wantFailure == "" {
						if tc.cpuTarget != nil || tc.memoryTarget != nil {
							tc.injectFailure = true
							admit, cpu, memory = setup()
							if result := admit(pod); result.Admit || !strings.Contains(result.Message, "injected provider failure") {
								t.Fatalf("recovered failure = %+v", result)
							}
							if cpu.State().GetDefaultCPUSet().String() != allocatedCPU || !reflect.DeepEqual(memory.State().GetMachineState(), allocatedMemory) {
								t.Fatal("rollback altered restored reservations")
							}
						}
						conflict := pod.DeepCopy()
						node := int32(0)
						if tc.legacy {
							conflict.Spec.NUMANode = &node
						} else {
							conflict.Spec.MemNUMA = ptr.To[int32](2)
							if expectedMemoryNode == "2" {
								conflict.Spec.MemNUMA = &node
							}
						}
						if result := admit(conflict); result.Admit || !strings.Contains(result.Message, "conflicts") {
							t.Fatalf("conflicting recovered placement = %+v", result)
						}
						config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
						lifecycle := &internalContainerLifecycleImpl{cpuManager: cpu, memoryManager: memory}
						if err := lifecycle.PreCreateContainer(logger, conflict, &conflict.Spec.Containers[0], config); err == nil {
							t.Fatal("conflicting checkpoint reached runtime masks")
						}
					}
					if tc.wantFailure == "" && (tc.legacy || tc.cpuTarget != nil || tc.memoryTarget != nil) {
						container := &pod.Spec.Containers[0]
						cpu.AddContainer(logger, pod, container, "cleanup")
						memory.AddContainer(logger, pod, container, "cleanup")
						addTrackedContainer(pod, container, "cleanup")
						if err := cpu.RemoveContainer(logger, "cleanup"); err != nil {
							t.Fatal(err)
						}
						if err := memory.RemoveContainer(logger, "cleanup"); err != nil {
							t.Fatal(err)
						}
						if err := removeTrackedContainer(logger, "cleanup"); err != nil {
							t.Fatal(err)
						}
						if !cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(memory.State().GetMachineState(), beforeMemory) {
							t.Fatal("normal cleanup leaked CPU or memory")
						}
						if store.GetAffinity(logger, string(pod.UID), "app").NUMANodeAffinity != nil || topologymanager.GetMemoryAffinity(store, logger, string(pod.UID), "app").NUMANodeAffinity != nil {
							t.Fatal("cleanup retained affinities")
						}
					}
				})
			}
		})
	}
}
