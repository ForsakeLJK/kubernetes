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
	"k8s.io/klog/v2/ktesting"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager"
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
			}{
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
				if tc.cpuTarget != nil && policyName != topologymanager.PolicyIndependentNUMA {
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
					directory := t.TempDir()
					setup := func() (func(*v1.Pod) lifecycle.PodAdmitResult, cpumanager.Manager, memorymanager.Manager) {
						t.Helper()
						numaInfo := &topologymanager.NUMAInfo{Nodes: []int{0, 2, 3}}
						policy := topologymanager.NewSingleNumaNodePolicy(numaInfo, topologymanager.PolicyOptions{})
						if policyName == topologymanager.PolicyIndependentNUMA {
							policy = topologymanager.NewIndependentNUMAPolicy(numaInfo, topologymanager.PolicyOptions{})
						}
						scope := topologymanager.NewPodScope(policy)
						var store topologymanager.Store = scope
						admit := func(pod *v1.Pod) lifecycle.PodAdmitResult { return scope.Admit(ctx, pod, lifecycle.AddOperation) }
						addProvider := scope.AddHintProvider
						// Explicit-file tests on macOS exercise portable allocation and Linux
						// mask construction; normal Linux tests also cover manager selection.
						if runtime.GOOS == "linux" {
							manager, err := topologymanager.NewManager(logger, info.Topology, policyName, "pod", nil)
							if err != nil {
								t.Fatal(err)
							}
							store = manager
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
						if tc.cpuTarget != nil && *tc.cpuTarget != *tc.memoryTarget {
							wantAligned = 0
						}
						if after-before != wantAligned {
							t.Fatalf("alignment metric increment = %v, want %v", after-before, wantAligned)
						}
						wantSplit := float64(0)
						if tc.cpuTarget != nil && *tc.cpuTarget != *tc.memoryTarget {
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
						if tc.cpuTarget != nil {
							wantCPU = "1"
							if *tc.cpuTarget == 2 {
								wantCPU = "8"
							}
							wantMemory = fmt.Sprint(*tc.memoryTarget)
						}
						if config.Linux.Resources.CpusetCpus != wantCPU || config.Linux.Resources.CpusetMems != wantMemory {
							t.Fatalf("runtime masks = %q/%q, want %q/%q", config.Linux.Resources.CpusetCpus, config.Linux.Resources.CpusetMems, wantCPU, wantMemory)
						}
						// Recreate both managers and topology affinity from their checkpoints.
						// A repeated admission must reuse concrete assignments and masks.
						if attempt == 0 && tc.dropCheckpoint != "" {
							if err := os.Remove(filepath.Join(directory, tc.dropCheckpoint)); err != nil {
								t.Fatal(err)
							}
						}
						admit, cpu, memory = setup()
					}
					if (tc.legacy || tc.cpuTarget != nil) && tc.wantFailure == "" {
						if tc.cpuTarget != nil {
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
							if *tc.memoryTarget == 2 {
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
				})
			}
		})
	}
}
