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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
)

const podWideMemoryUnit = uint64(256 * 1024 * 1024)

func podWideContainer(name string, units int, sidecar bool) v1.Container {
	resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse(fmt.Sprint(units)), v1.ResourceMemory: *resource.NewQuantity(int64(units)*int64(podWideMemoryUnit), resource.BinarySI)}
	c := v1.Container{Name: name, Resources: v1.ResourceRequirements{Requests: resources.DeepCopy(), Limits: resources.DeepCopy()}}
	if sidecar {
		c.RestartPolicy = ptr.To(v1.ContainerRestartPolicyAlways)
	}
	return c
}

type podWideNUMAFixture struct {
	t            *testing.T
	ctx          context.Context
	logger       klog.Logger
	directory    string
	distances    map[int][]uint64
	failAfterCPU string
	scope        topologymanager.Scope
	cpu          cpumanager.Manager
	memory       memorymanager.Manager
}

func newPodWideNUMAFixture(t *testing.T) *podWideNUMAFixture {
	logger, ctx := ktesting.NewTestContext(t)
	f := &podWideNUMAFixture{t: t, ctx: ctx, logger: logger, directory: t.TempDir(), distances: map[int][]uint64{0: {10, 20, 40}, 2: {40, 10, 20}}}
	f.restart()
	return f
}

func (f *podWideNUMAFixture) restart() {
	f.t.Helper()
	info := &cadvisorapi.MachineInfo{NumCores: 12, Topology: []cadvisorapi.Node{{Id: 0, Memory: 16 * podWideMemoryUnit}, {Id: 2, Memory: 2 * podWideMemoryUnit}, {Id: 3, Memory: 4 * podWideMemoryUnit}}}
	for cpu := 0; cpu < 12; cpu++ {
		index := 0
		if cpu >= 8 {
			index = 1
		}
		info.Topology[index].Cores = append(info.Topology[index].Cores, cadvisorapi.Core{Id: cpu, SocketID: index, Threads: []int{cpu}})
	}
	f.scope = topologymanager.NewPodScope(topologymanager.NewIndependentNUMAPolicy(&topologymanager.NUMAInfo{Nodes: []int{0, 2, 3}, NUMADistances: f.distances}, topologymanager.PolicyOptions{}))
	reservations := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")}
	var err error
	f.cpu, err = cpumanager.NewManager(f.logger, "static", map[string]string{"strict-cpu-reservation": "true"}, time.Hour, info, cpuset.New(0), reservations, f.directory, f.scope)
	if err != nil {
		f.t.Fatal(err)
	}
	f.memory, err = memorymanager.NewManager(f.logger, "Static", info, reservations, []kubeletconfig.MemoryReservation{
		{NumaNode: 0, Limits: v1.ResourceList{v1.ResourceMemory: resource.MustParse("512Mi")}},
		{NumaNode: 2, Limits: v1.ResourceList{v1.ResourceMemory: resource.MustParse("512Mi")}},
	}, f.directory, f.scope)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.cpu.Start(f.ctx, func() []*v1.Pod { return nil }, numaTestSourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
		f.t.Fatal(err)
	}
	if err := f.memory.Start(f.ctx, func() []*v1.Pod { return nil }, numaTestSourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
		f.t.Fatal(err)
	}
	f.scope.AddHintProvider(f.logger, f.cpu)
	if f.failAfterCPU != "" {
		f.scope.AddHintProvider(f.logger, podWideFailure{container: f.failAfterCPU})
	}
	f.scope.AddHintProvider(f.logger, f.memory)
}

func (f *podWideNUMAFixture) admit(pod *v1.Pod) lifecycle.PodAdmitResult {
	return f.scope.Admit(f.ctx, pod, lifecycle.AddOperation)
}

func (f *podWideNUMAFixture) masks(pod *v1.Pod, containers []v1.Container) {
	f.t.Helper()
	hook := &internalContainerLifecycleImpl{cpuManager: f.cpu, memoryManager: f.memory}
	for _, c := range containers {
		for attempt := 0; attempt < 2; attempt++ {
			config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
			if err := hook.PreCreateContainer(f.logger, pod, &c, config); err != nil {
				f.t.Fatalf("%s pre-start: %v", c.Name, err)
			}
			cpus, err := cpuset.Parse(config.Linux.Resources.CpusetCpus)
			if err != nil {
				f.t.Fatal(err)
			}
			if cpus.Size() != int(c.Resources.Requests.Cpu().Value()) || !cpus.IsSubsetOf(cpuset.New(8, 9, 10, 11)) || config.Linux.Resources.CpusetMems != "3" {
				f.t.Fatalf("%s masks = %+v", c.Name, config.Linux.Resources)
			}
		}
	}
}

func (f *podWideNUMAFixture) remove(pod *v1.Pod, c v1.Container) {
	f.t.Helper()
	id := string(pod.UID) + "/" + c.Name
	f.cpu.AddContainer(f.logger, pod, &c, id)
	f.memory.AddContainer(f.logger, pod, &c, id)
	f.scope.AddContainer(pod, &c, id)
	if err := f.cpu.RemoveContainer(f.logger, id); err != nil {
		f.t.Fatal(err)
	}
	if err := f.memory.RemoveContainer(f.logger, id); err != nil {
		f.t.Fatal(err)
	}
	if err := f.scope.RemoveContainer(f.logger, id); err != nil {
		f.t.Fatal(err)
	}
}

func podWidePod(form string) *v1.Pod {
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod-wide"}, Spec: v1.PodSpec{CPUNUMA: ptr.To[int32](2), MemNUMA: ptr.To[int32](3), Containers: []v1.Container{podWideContainer("app-a", 1, false), podWideContainer("app-b", 1, false)}}}
	if form == "CPU-only" {
		pod.Spec.MemNUMA = nil
	}
	if form == "memory-only" {
		pod.Spec.CPUNUMA = nil
	}
	return pod
}

func TestPodWideIndependentNUMA(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, workload := range []string{"applications", "sequential init", "sidecar overlap"} {
			t.Run(form+"/"+workload, func(t *testing.T) {
				f := newPodWideNUMAFixture(t)
				pod := podWidePod(form)
				peak := 2
				if workload == "sequential init" {
					pod.Spec.InitContainers = []v1.Container{podWideContainer("init-a", 3, false), podWideContainer("init-b", 2, false)}
					peak = 3
				}
				if workload == "sidecar overlap" {
					pod.Spec.InitContainers = []v1.Container{podWideContainer("sidecar-a", 1, true), podWideContainer("init", 3, false), podWideContainer("sidecar-b", 1, true)}
					peak = 4
				}
				beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				all := append(pod.Spec.InitContainers, pod.Spec.Containers...)
				f.masks(pod, all)
				if beforeCPU.Difference(f.cpu.State().GetDefaultCPUSet()).Size() != peak || f.memory.State().GetMachineState()[3].MemoryMap[v1.ResourceMemory].Reserved != uint64(peak)*podWideMemoryUnit {
					t.Fatal("effective resources lost init reuse or sidecar overlap")
				}
				cpuAssignments, memoryAssignments := f.cpu.State().GetCPUAssignments(), f.memory.State().GetMemoryAssignments()
				for attempt := 0; attempt < 2; attempt++ {
					f.restart()
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("recovery = %+v", result)
					}
					f.masks(pod, all)
					if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), cpuAssignments) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), memoryAssignments) {
						t.Fatal("recovery changed surviving assignments")
					}
				}
				for _, c := range all {
					f.remove(pod, c)
				}
				if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) {
					t.Fatal("cleanup leaked resources")
				}
				for _, c := range all {
					if f.scope.GetAffinity(f.logger, string(pod.UID), c.Name).NUMANodeAffinity != nil || topologymanager.GetMemoryAffinity(f.scope, f.logger, string(pod.UID), c.Name).NUMANodeAffinity != nil {
						t.Fatal("cleanup retained affinities")
					}
				}
			})
		}
	}
}

type podWideFailure struct {
	topologymanager.HintProvider
	container string
}

func (p podWideFailure) GetPodTopologyHints(klog.Logger, *v1.Pod, lifecycle.Operation) map[string][]topologymanager.TopologyHint {
	return nil
}
func (p podWideFailure) Allocate(_ context.Context, _ *v1.Pod, c *v1.Container, _ lifecycle.Operation) error {
	if c.Name == p.container {
		return fmt.Errorf("injected failure for %s", c.Name)
	}
	return nil
}

func TestPodWideIndependentNUMARollback(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, failContainer := range []string{"init-a", "sidecar", "init-b", "app-a", "app-b"} {
			for _, boundary := range []string{"CPU", "memory"} {
				t.Run(form+"/"+failContainer+"/"+boundary, func(t *testing.T) {
					f := newPodWideNUMAFixture(t)
					pod := podWidePod(form)
					pod.Spec.InitContainers = []v1.Container{podWideContainer("init-a", 3, false), podWideContainer("sidecar", 1, true), podWideContainer("init-b", 2, false)}
					beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
					if boundary == "CPU" {
						f.failAfterCPU = failContainer
						f.restart()
					} else {
						f.scope.AddHintProvider(f.logger, podWideFailure{container: failContainer})
					}
					for attempt := 0; attempt < 3; attempt++ {
						result := f.admit(pod)
						if result.Admit || !strings.Contains(result.Message, "injected failure") {
							t.Fatalf("admission = %+v", result)
						}
						if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) || len(f.cpu.State().GetCPUAssignments()) != 0 || len(f.memory.State().GetMemoryAssignments()) != 0 {
							t.Fatal("rollback leaked resources")
						}
					}
					f.failAfterCPU = ""
					f.restart()
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("admission after rollback = %+v", result)
					}
					f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
				})
			}
		}
	}
}

func TestPodWideIndependentNUMAReleasedInitRecovery(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		t.Run(form, func(t *testing.T) {
			f := newPodWideNUMAFixture(t)
			pod := podWidePod(form)
			pod.Spec.InitContainers = []v1.Container{podWideContainer("init", 3, false), podWideContainer("sidecar", 1, true)}
			if result := f.admit(pod); !result.Admit {
				t.Fatalf("admission = %+v", result)
			}
			f.remove(pod, pod.Spec.InitContainers[0])
			pod.Status.InitContainerStatuses = []v1.ContainerStatus{{Name: "init", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}}}
			beforeCPU, beforeMemory := f.cpu.State().GetCPUAssignments(), f.memory.State().GetMemoryAssignments()
			f.restart()
			for attempt := 0; attempt < 2; attempt++ {
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("released init recovery = %+v", result)
				}
				f.masks(pod, append(pod.Spec.InitContainers[1:], pod.Spec.Containers...))
				if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), beforeMemory) {
					t.Fatal("recovery recreated released init allocations")
				}
			}
		})
	}
}

func TestPodWideIndependentNUMAConflictingRecovery(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, broken := range []string{"CPU", "memory"} {
			t.Run(form+"/"+broken, func(t *testing.T) {
				f := newPodWideNUMAFixture(t)
				pod := podWidePod(form)
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				if broken == "CPU" {
					state, err := cpustate.NewCheckpointState(f.logger, f.directory, "cpu_manager_state", "static", containermap.NewContainerMap())
					if err != nil {
						t.Fatal(err)
					}
					old, _ := state.GetCPUSet(string(pod.UID), "app-b")
					state.SetDefaultCPUSet(state.GetDefaultCPUSet().Union(old).Difference(cpuset.New(1)))
					state.SetCPUSet(string(pod.UID), "app-b", cpuset.New(1))
				} else {
					state, err := memorystate.NewCheckpointState(f.logger, f.directory, "memory_manager_state", "Static")
					if err != nil {
						t.Fatal(err)
					}
					blocks := state.GetMemoryBlocks(string(pod.UID), "app-b")
					machine := state.GetMachineState()
					machine[3].NumberOfAssignments--
					machine[0].NumberOfAssignments++
					machine[3].MemoryMap[v1.ResourceMemory].Reserved -= blocks[0].Size
					machine[3].MemoryMap[v1.ResourceMemory].Free += blocks[0].Size
					machine[0].MemoryMap[v1.ResourceMemory].Reserved += blocks[0].Size
					machine[0].MemoryMap[v1.ResourceMemory].Free -= blocks[0].Size
					blocks[0].NUMAAffinity = []int{0}
					state.SetMachineState(machine)
					state.SetMemoryBlocks(string(pod.UID), "app-b", blocks)
				}
				f.restart()
				beforeCPU, beforeMemory := f.cpu.State().GetCPUAssignments(), f.memory.State().GetMemoryAssignments()
				hook := &internalContainerLifecycleImpl{cpuManager: f.cpu, memoryManager: f.memory}
				config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
				if err := hook.PreCreateContainer(f.logger, pod, &pod.Spec.Containers[0], config); err == nil {
					t.Fatal("conflicting sibling assignment reached runtime")
				}
				if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "conflict") {
					t.Fatalf("conflicting recovery = %+v", result)
				}
				if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), beforeMemory) {
					t.Fatal("conflicting recovery changed assignments")
				}
			})
		}
	}
}

func TestPodWideIndependentNUMAPartialCPURecovery(t *testing.T) {
	f := newPodWideNUMAFixture(t)
	pod := podWidePod("memory-only")
	pod.Spec.InitContainers = []v1.Container{podWideContainer("init", 3, false)}
	if result := f.admit(pod); !result.Admit {
		t.Fatalf("admission = %+v", result)
	}
	state, err := cpustate.NewCheckpointState(f.logger, f.directory, "cpu_manager_state", "static", containermap.NewContainerMap())
	if err != nil {
		t.Fatal(err)
	}
	// A valid surviving app may own the CPU the missing earlier app used.
	// Init CPUs overlap apps, so the missing assignment cannot use that CPU.
	state.Delete(string(pod.UID), "app-a")
	state.SetCPUSet(string(pod.UID), "app-b", cpuset.New(8))
	f.restart()
	beforeCPU, beforeMemory := f.cpu.State().GetCPUAssignments(), f.memory.State().GetMemoryAssignments()
	f.scope.AddHintProvider(f.logger, podWideFailure{container: "app-b"})
	for attempt := 0; attempt < 3; attempt++ {
		if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "injected failure") {
			t.Fatalf("partial failure = %+v", result)
		}
		if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), beforeMemory) {
			t.Fatal("partial rollback changed surviving assignments")
		}
	}
	f.restart()
	if result := f.admit(pod); !result.Admit {
		t.Fatalf("partial recovery = %+v", result)
	}
	a, _ := f.cpu.State().GetCPUSet(string(pod.UID), "app-a")
	b, _ := f.cpu.State().GetCPUSet(string(pod.UID), "app-b")
	if !a.Intersection(b).IsEmpty() {
		t.Fatal("partial recovery assigned the same CPU to simultaneous applications")
	}
	f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
}

func TestPodWideIndependentNUMAInitOnlyRestoration(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, form := range []string{"CPU-only", "memory-only"} {
			t.Run(fmt.Sprintf("%s/completed-init-%t", form, completed), func(t *testing.T) {
				f := newPodWideNUMAFixture(t)
				pod := podWidePod(form)
				pod.Spec.InitContainers = []v1.Container{podWideContainer("init", 3, false)}
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				cpuState, err := cpustate.NewCheckpointState(f.logger, f.directory, "cpu_manager_state", "static", containermap.NewContainerMap())
				if err != nil {
					t.Fatal(err)
				}
				memoryState, err := memorystate.NewCheckpointState(f.logger, f.directory, "memory_manager_state", "Static")
				if err != nil {
					t.Fatal(err)
				}
				machine := memoryState.GetMachineState()
				for _, c := range pod.Spec.Containers {
					cpuState.Delete(string(pod.UID), c.Name)
					for _, block := range memoryState.GetMemoryBlocks(string(pod.UID), c.Name) {
						node := machine[block.NUMAAffinity[0]]
						node.NumberOfAssignments--
						node.MemoryMap[block.Type].Reserved -= block.Size
						node.MemoryMap[block.Type].Free += block.Size
					}
					memoryState.Delete(string(pod.UID), c.Name)
				}
				memoryState.SetMachineState(machine)
				if completed {
					pod.Status.InitContainerStatuses = []v1.ContainerStatus{{Name: "init", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}}}
				}
				f.distances = map[int][]uint64{0: {10, 20, 11}, 2: {11, 10, 50}}
				f.restart()
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("init-only restoration = %+v", result)
				}
				f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
			})
		}
	}
}

func TestPodWideIndependentNUMAPartialManagerRestoration(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, missing := range []string{"cpu_manager_state", "memory_manager_state"} {
			t.Run(form+"/"+missing, func(t *testing.T) {
				f := newPodWideNUMAFixture(t)
				pod := podWidePod(form)
				pod.Spec.InitContainers = []v1.Container{podWideContainer("init", 3, false), podWideContainer("sidecar", 1, true)}
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				if err := os.Remove(filepath.Join(f.directory, missing)); err != nil {
					t.Fatal(err)
				}
				f.restart()
				beforeCPU, beforeMemory := f.cpu.State().GetCPUAssignments(), f.memory.State().GetMemoryAssignments()
				beforePool, beforeMachine := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
				f.scope.AddHintProvider(f.logger, podWideFailure{container: "app-b"})
				for attempt := 0; attempt < 3; attempt++ {
					if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "injected failure") {
						t.Fatalf("partial failure = %+v", result)
					}
					if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), beforeMemory) || !f.cpu.State().GetDefaultCPUSet().Equals(beforePool) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMachine) {
						t.Fatal("partial rollback changed surviving assignments or leaked capacity")
					}
				}
				f.restart()
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("partial recovery = %+v", result)
				}
				f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
			})
		}
	}
}

func TestPodWideIndependentNUMACapacity(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, short := range []string{"CPU", "memory", "sidecar"} {
			t.Run(form+"/"+short, func(t *testing.T) {
				f := newPodWideNUMAFixture(t)
				pod := podWidePod(form)
				pod.Spec.Containers[0] = podWideContainer("app-a", 3, false)
				pod.Spec.Containers[1] = podWideContainer("app-b", 2, false)
				if short == "memory" {
					for i := range pod.Spec.Containers {
						pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse("1")
						pod.Spec.Containers[i].Resources.Limits[v1.ResourceCPU] = resource.MustParse("1")
					}
					if form == "CPU-only" {
						for i := range pod.Spec.Containers {
							pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse("2Gi")
							pod.Spec.Containers[i].Resources.Limits[v1.ResourceMemory] = resource.MustParse("2Gi")
						}
					}
				} else {
					for i := range pod.Spec.Containers {
						pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse("256Mi")
						pod.Spec.Containers[i].Resources.Limits[v1.ResourceMemory] = resource.MustParse("256Mi")
					}
					if form == "memory-only" {
						for i := range pod.Spec.Containers {
							pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse("4")
							pod.Spec.Containers[i].Resources.Limits[v1.ResourceCPU] = resource.MustParse("4")
						}
					}
					if short == "sidecar" {
						pod.Spec.Containers = []v1.Container{podWideContainer("app-a", 3, false)}
						pod.Spec.InitContainers = []v1.Container{podWideContainer("sidecar", 2, true)}
						if form == "memory-only" {
							pod.Spec.Containers = []v1.Container{podWideContainer("app-a", 5, false)}
							pod.Spec.InitContainers = []v1.Container{podWideContainer("sidecar", 3, true)}
						}
					}
					if short == "sidecar" {
						for _, list := range [][]v1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
							for i := range list {
								list[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse("256Mi")
								list[i].Resources.Limits[v1.ResourceMemory] = resource.MustParse("256Mi")
							}
						}
					}
				}
				beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
				for attempt := 0; attempt < 3; attempt++ {
					if result := f.admit(pod); result.Admit {
						t.Fatal("aggregate or overlapping resources exceeded target capacity")
					}
					if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) {
						t.Fatal("capacity refusal leaked reservations")
					}
				}
			})
		}
	}
}

func TestPodWideIndependentNUMAUnfinishedInitRecovery(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, sidecar := range []string{"none", "before", "after", "later init"} {
			t.Run(form+"/"+sidecar, func(t *testing.T) {
				f := newPodWideNUMAFixture(t)
				pod := podWidePod(form)
				init := podWideContainer("init", 3, false)
				pod.Spec.InitContainers = []v1.Container{init}
				peak := uint64(3)
				if sidecar == "before" {
					pod.Spec.InitContainers = append([]v1.Container{podWideContainer("sidecar", 1, true)}, init)
					peak = 4
				}
				if sidecar == "after" {
					pod.Spec.InitContainers = append(pod.Spec.InitContainers, podWideContainer("sidecar", 1, true))
				}
				if sidecar == "later init" {
					init = podWideContainer("init", 4, false)
					pod.Spec.InitContainers = []v1.Container{init, podWideContainer("later-init", 3, false)}
					peak = 4
				}
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				f.remove(pod, init)
				beforeCPU, beforeMemory := f.cpu.State().GetCPUAssignments(), f.memory.State().GetMemoryAssignments()
				f.restart()
				beforePool, beforeMachine := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
				f.scope.AddHintProvider(f.logger, podWideFailure{container: "app-b"})
				for attempt := 0; attempt < 3; attempt++ {
					if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "injected failure") {
						t.Fatalf("unfinished init rollback = %+v", result)
					}
					if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), beforeMemory) || !f.cpu.State().GetDefaultCPUSet().Equals(beforePool) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMachine) {
						t.Fatal("unfinished init rollback changed future assignments or leaked capacity")
					}
				}
				f.restart()
				for attempt := 0; attempt < 2; attempt++ {
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("unfinished init recovery = %+v", result)
					}
					f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
					for name, cpus := range beforeCPU[string(pod.UID)] {
						got, _ := f.cpu.State().GetCPUSet(string(pod.UID), name)
						if !got.Equals(cpus) || !reflect.DeepEqual(f.memory.State().GetMemoryBlocks(string(pod.UID), name), beforeMemory[string(pod.UID)][name]) {
							t.Fatal("init recovery changed future assignments")
						}
					}
					if f.memory.State().GetMachineState()[3].MemoryMap[v1.ResourceMemory].Reserved != peak*podWideMemoryUnit {
						t.Fatal("init recovery double-counted future memory reservations")
					}
					f.restart()
				}
			})
		}
	}
}
