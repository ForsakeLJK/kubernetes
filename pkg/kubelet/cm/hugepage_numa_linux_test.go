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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	cpustate "k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	memorystate "k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
)

var numaHugepageUnits = map[v1.ResourceName]uint64{
	v1.ResourceMemory: podWideMemoryUnit,
	"hugepages-2Mi":   2 << 20,
	"hugepages-1Gi":   1 << 30,
}

func numaHugepageCapacity() map[int][]cadvisorapi.HugePagesInfo {
	return map[int][]cadvisorapi.HugePagesInfo{
		0: {{PageSize: 2048, NumPages: 16}, {PageSize: 1048576, NumPages: 16}},
		2: {{PageSize: 2048, NumPages: 2}, {PageSize: 1048576, NumPages: 2}},
		3: {{PageSize: 2048, NumPages: 4}, {PageSize: 1048576, NumPages: 4}},
	}
}

func addNUMAHugepages(pod *v1.Pod) {
	for _, containers := range [][]v1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range containers {
			c := &containers[i]
			for name, size := range numaHugepageUnits {
				if name == v1.ResourceMemory {
					continue
				}
				amount := *resource.NewQuantity(c.Resources.Requests.Cpu().Value()*int64(size), resource.BinarySI)
				c.Resources.Requests[name], c.Resources.Limits[name] = amount, amount
			}
		}
	}
}

func checkNUMAMemoryPeak(t *testing.T, f *podWideNUMAFixture, peak uint64) {
	t.Helper()
	machine := f.memory.State().GetMachineState()
	for name, unit := range numaHugepageUnits {
		if got := machine[3].MemoryMap[name].Reserved; got != peak*unit {
			t.Fatalf("%s reserved %d, want %d", name, got, peak*unit)
		}
	}
}

func TestIndependentNUMAHugepageSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		form    string
		missing v1.ResourceName
		split   bool
		want    string
	}{
		{name: "automatic skips missing size", form: "CPU-only", missing: "hugepages-2Mi"},
		{name: "automatic skips other size", form: "CPU-only", missing: "hugepages-1Gi"},
		{name: "explicit missing size", form: "explicit", missing: "hugepages-2Mi", want: "hugepages-2Mi"},
		{name: "memory-only missing size", form: "memory-only", missing: "hugepages-1Gi", want: "hugepages-1Gi"},
		{name: "automatic cannot split sizes", form: "CPU-only", split: true, want: "no feasible singleton memory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := numaHugepageCapacity()
			if tc.split {
				pages[0] = pages[0][:1]
				pages[2] = pages[2][1:]
				pages[3] = pages[3][1:]
			} else if tc.form == "CPU-only" {
				if tc.missing == "hugepages-2Mi" {
					pages[0] = pages[0][1:]
				} else {
					pages[0] = pages[0][:1]
				}
			} else {
				if tc.missing == "hugepages-2Mi" {
					pages[3] = pages[3][1:]
				} else {
					pages[3] = pages[3][:1]
				}
			}
			f := newPodWideNUMAFixtureWithHugepages(t, pages)
			pod := podWidePod(tc.form)
			addNUMAHugepages(pod)
			beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
			for attempt := 0; attempt < 3; attempt++ {
				result := f.admit(pod)
				if tc.want != "" {
					if result.Admit || !strings.Contains(result.Message, tc.want) {
						t.Fatalf("admission = %+v, want %s", result, tc.want)
					}
					if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) {
						t.Fatal("refusal leaked resources")
					}
					continue
				}
				if !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				f.masks(pod, pod.Spec.Containers)
				checkNUMAMemoryPeak(t, f, 2)
			}
		})
	}
}

func TestIndependentNUMAHugepageLifetimes(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, workload := range []string{"applications", "sequential init", "sidecar overlap", "init-only sizes", "sidecar-only sizes"} {
			t.Run(form+"/"+workload, func(t *testing.T) {
				f := newPodWideNUMAFixtureWithHugepages(t, numaHugepageCapacity())
				pod := podWidePod(form)
				peak := uint64(2)
				if workload == "sequential init" || workload == "init-only sizes" {
					pod.Spec.InitContainers = []v1.Container{podWideContainer("init-a", 3, false), podWideContainer("init-b", 2, false)}
					peak = 3
				}
				if workload == "sidecar overlap" || workload == "sidecar-only sizes" {
					pod.Spec.InitContainers = []v1.Container{podWideContainer("sidecar-a", 1, true), podWideContainer("init", 3, false), podWideContainer("sidecar-b", 1, true)}
					peak = 4
				}
				addNUMAHugepages(pod)
				if strings.Contains(workload, "only sizes") {
					for i := range pod.Spec.Containers {
						delete(pod.Spec.Containers[i].Resources.Requests, "hugepages-2Mi")
						delete(pod.Spec.Containers[i].Resources.Limits, "hugepages-2Mi")
					}
					if workload == "sidecar-only sizes" {
						delete(pod.Spec.InitContainers[1].Resources.Requests, "hugepages-2Mi")
						delete(pod.Spec.InitContainers[1].Resources.Limits, "hugepages-2Mi")
					}
				}
				beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				all := append(pod.Spec.InitContainers, pod.Spec.Containers...)
				f.masks(pod, all)
				for name, unit := range numaHugepageUnits {
					expected := peak * unit
					if workload == "sidecar-only sizes" && name == "hugepages-2Mi" {
						expected = 2 * unit
					}
					if got := f.memory.State().GetMachineState()[3].MemoryMap[name].Reserved; got != expected {
						t.Fatalf("%s reserved %d, want %d", name, got, expected)
					}
				}
				assignments := f.memory.State().GetMemoryAssignments()
				for attempt := 0; attempt < 2; attempt++ {
					f.restart()
					// Restored resources remain mandatory when another node becomes nearer.
					f.distances = map[int][]uint64{0: {10, 20, 11}, 2: {11, 10, 50}}
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("recovery = %+v", result)
					}
					f.masks(pod, all)
					if !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), assignments) {
						t.Fatal("recovery changed hugepage assignments")
					}
				}
				for _, c := range all {
					f.remove(pod, c)
				}
				if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) {
					t.Fatal("cleanup leaked memory group")
				}
			})
		}
	}
}

type numaHugepageSnapshot struct {
	cpu     cpustate.ContainerCPUAssignments
	memory  memorystate.ContainerMemoryAssignments
	pool    cpuset.CPUSet
	machine memorystate.NUMANodeMap
}

func snapshotNUMAHugepages(f *podWideNUMAFixture) numaHugepageSnapshot {
	return numaHugepageSnapshot{cpu: f.cpu.State().GetCPUAssignments(), memory: f.memory.State().GetMemoryAssignments(), pool: f.cpu.State().GetDefaultCPUSet(), machine: f.memory.State().GetMachineState()}
}

func (s numaHugepageSnapshot) assertUnchanged(f *podWideNUMAFixture) {
	f.t.Helper()
	if !reflect.DeepEqual(f.cpu.State().GetCPUAssignments(), s.cpu) || !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), s.memory) || !f.cpu.State().GetDefaultCPUSet().Equals(s.pool) || !reflect.DeepEqual(f.memory.State().GetMachineState(), s.machine) {
		f.t.Fatal("rollback changed surviving allocations or leaked reservations")
	}
}

func TestIndependentNUMAHugepageRollback(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, boundary := range []string{"CPU", "memory"} {
			for _, fail := range []string{"init-a", "sidecar", "init-b", "app-a", "app-b"} {
				t.Run(form+"/"+boundary+"/"+fail, func(t *testing.T) {
					f := newPodWideNUMAFixtureWithHugepages(t, numaHugepageCapacity())
					control := podWidePod("explicit")
					control.UID = "unrelated"
					control.Spec.CPUNUMA, control.Spec.MemNUMA = ptr.To[int32](0), ptr.To[int32](0)
					addNUMAHugepages(control)
					if result := f.admit(control); !result.Admit {
						t.Fatalf("control = %+v", result)
					}
					pod := podWidePod(form)
					pod.Spec.InitContainers = []v1.Container{podWideContainer("init-a", 3, false), podWideContainer("sidecar", 1, true), podWideContainer("init-b", 2, false)}
					addNUMAHugepages(pod)
					if boundary == "CPU" {
						f.failAfterCPU = fail
						f.restart()
					} else {
						f.scope.AddHintProvider(f.logger, podWideFailure{container: fail})
					}
					before := snapshotNUMAHugepages(f)
					for attempt := 0; attempt < 3; attempt++ {
						if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "injected failure") {
							t.Fatalf("failure = %+v", result)
						}
						before.assertUnchanged(f)
						for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
							if f.scope.GetAffinity(f.logger, string(pod.UID), c.Name).NUMANodeAffinity != nil || topologymanager.GetMemoryAffinity(f.scope, f.logger, string(pod.UID), c.Name).NUMANodeAffinity != nil {
								t.Fatal("rollback retained an affinity")
							}
						}
					}
					f.failAfterCPU = ""
					f.restart()
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("retry = %+v", result)
					}
					f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
					checkNUMAMemoryPeak(t, f, 3)
				})
			}
		}
	}
}

func TestIndependentNUMAHugepagePartialRecovery(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, missing := range []string{"cpu_manager_state", "memory_manager_state", "unfinished init", "completed init"} {
			t.Run(form+"/"+missing, func(t *testing.T) {
				f := newPodWideNUMAFixtureWithHugepages(t, numaHugepageCapacity())
				pod := podWidePod(form)
				pod.Spec.InitContainers = []v1.Container{podWideContainer("sidecar-a", 1, true), podWideContainer("init", 3, false), podWideContainer("sidecar-b", 1, true)}
				addNUMAHugepages(pod)
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("admission = %+v", result)
				}
				if strings.HasSuffix(missing, "init") {
					f.remove(pod, pod.Spec.InitContainers[1])
					if missing == "completed init" {
						pod.Status.InitContainerStatuses = []v1.ContainerStatus{{Name: "init", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}}}
					}
				} else if err := os.Remove(filepath.Join(f.directory, missing)); err != nil {
					t.Fatal(err)
				}
				f.restart()
				before := snapshotNUMAHugepages(f)
				f.scope.AddHintProvider(f.logger, podWideFailure{container: "app-b"})
				for attempt := 0; attempt < 3; attempt++ {
					if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "injected failure") {
						t.Fatalf("recovery failure = %+v", result)
					}
					before.assertUnchanged(f)
				}
				f.restart()
				for attempt := 0; attempt < 2; attempt++ {
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("recovery = %+v", result)
					}
					all := append(pod.Spec.InitContainers, pod.Spec.Containers...)
					if missing == "completed init" {
						all = append([]v1.Container{pod.Spec.InitContainers[0], pod.Spec.InitContainers[2]}, pod.Spec.Containers...)
					}
					f.masks(pod, all)
					checkNUMAMemoryPeak(t, f, 4)
					for name, blocks := range before.memory[string(pod.UID)] {
						if !reflect.DeepEqual(f.memory.State().GetMemoryBlocks(string(pod.UID), name), blocks) {
							t.Fatal("recovery changed surviving memory blocks")
						}
					}
					if missing == "completed init" && len(f.memory.State().GetMemoryBlocks(string(pod.UID), "init")) != 0 {
						t.Fatal("recovery recreated completed init hugepages")
					}
					f.restart()
				}
			})
		}
	}
}

func TestIndependentNUMAHugepageConflictingRecovery(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, name := range []v1.ResourceName{"hugepages-2Mi", "hugepages-1Gi"} {
			for _, fault := range []string{"remote", "multi-node", "missing", "duplicate", "wrong size"} {
				t.Run(form+"/"+string(name)+"/"+fault, func(t *testing.T) {
					f := newPodWideNUMAFixtureWithHugepages(t, numaHugepageCapacity())
					pod := podWidePod(form)
					addNUMAHugepages(pod)
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("admission = %+v", result)
					}
					checkpoint, err := memorystate.NewCheckpointState(f.logger, f.directory, "memory_manager_state", "Static")
					if err != nil {
						t.Fatal(err)
					}
					blocks := checkpoint.GetMemoryBlocks(string(pod.UID), "app-b")
					machine := checkpoint.GetMachineState()
					for i, b := range blocks {
						if b.Type != name {
							continue
						}
						switch fault {
						case "remote":
							blocks[i].NUMAAffinity = []int{0}
							machine[3].NumberOfAssignments--
							machine[0].NumberOfAssignments++
							machine[3].MemoryMap[name].Reserved -= b.Size
							machine[3].MemoryMap[name].Free += b.Size
							machine[0].MemoryMap[name].Reserved += b.Size
							machine[0].MemoryMap[name].Free -= b.Size
						case "multi-node":
							blocks[i].NUMAAffinity = []int{0, 3}
						case "missing":
							blocks = append(blocks[:i], blocks[i+1:]...)
							machine[3].NumberOfAssignments--
							machine[3].MemoryMap[name].Reserved -= b.Size
							machine[3].MemoryMap[name].Free += b.Size
						case "duplicate":
							blocks = append(blocks, b)
							machine[3].NumberOfAssignments++
							machine[3].MemoryMap[name].Reserved += b.Size
							machine[3].MemoryMap[name].Free -= b.Size
						case "wrong size":
							blocks[i].Size = 0
							machine[3].MemoryMap[name].Reserved -= b.Size
							machine[3].MemoryMap[name].Free += b.Size
						}
						break
					}
					checkpoint.SetMachineState(machine)
					checkpoint.SetMemoryBlocks(string(pod.UID), "app-b", blocks)
					// The manager's validation seam refuses inconsistent blocks before start.
					// Multi-node corruption is also rejected by static-policy startup.
					if fault == "multi-node" {
						f.memory.State().(memorystate.State).SetMemoryBlocks(string(pod.UID), "app-b", blocks)
					} else {
						f.restart()
					}
					before := f.memory.State().GetMemoryAssignments()
					hook := &internalContainerLifecycleImpl{cpuManager: f.cpu, memoryManager: f.memory}
					config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
					if err := hook.PreCreateContainer(f.logger, pod, &pod.Spec.Containers[1], config); err == nil {
						t.Fatal("invalid hugepage assignment reached runtime")
					}
					if result := f.admit(pod); result.Admit {
						t.Fatal("invalid hugepage assignment was admitted")
					}
					if !reflect.DeepEqual(f.memory.State().GetMemoryAssignments(), before) {
						t.Fatal("recovery repaired conflicting checkpoint")
					}
				})
			}
		}
	}
}

func TestIndependentNUMAHugepageCapacity(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, name := range []v1.ResourceName{"hugepages-2Mi", "hugepages-1Gi"} {
			t.Run(form+"/"+string(name), func(t *testing.T) {
				pages := numaHugepageCapacity()
				if form == "CPU-only" {
					pages[0][0].NumPages = 4
					pages[0][1].NumPages = 4
				}
				f := newPodWideNUMAFixtureWithHugepages(t, pages)
				pod := podWidePod(form)
				pod.Spec.InitContainers = []v1.Container{podWideContainer("sidecar", 1, true), podWideContainer("init", 1, false)}
				addNUMAHugepages(pod)
				amount := *resource.NewQuantity(int64(4*numaHugepageUnits[name]), resource.BinarySI)
				pod.Spec.InitContainers[1].Resources.Requests[name], pod.Spec.InitContainers[1].Resources.Limits[name] = amount, amount
				beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
				for attempt := 0; attempt < 3; attempt++ {
					result := f.admit(pod)
					want := string(name)
					if form == "CPU-only" {
						want = "no feasible singleton memory"
					}
					if result.Admit || !strings.Contains(result.Message, want) {
						t.Fatalf("capacity = %+v, want %s", result, want)
					}
					if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) {
						t.Fatal("capacity refusal leaked reservations")
					}
				}
			})
		}
	}
}

func TestIndependentNUMAHugepageCompatibility(t *testing.T) {
	for _, form := range []string{"ordinary", "legacy", "equal targets"} {
		t.Run(form, func(t *testing.T) {
			f := newPodWideNUMAFixtureWithHugepages(t, numaHugepageCapacity())
			pod := podWidePod("explicit")
			pod.Spec.CPUNUMA, pod.Spec.MemNUMA = nil, nil
			if form == "legacy" {
				pod.Spec.NUMANode = ptr.To[int32](0)
			}
			if form == "equal targets" {
				pod.Spec.CPUNUMA, pod.Spec.MemNUMA = ptr.To[int32](0), ptr.To[int32](0)
			}
			addNUMAHugepages(pod)
			beforeCPU, beforeMemory := f.cpu.State().GetDefaultCPUSet(), f.memory.State().GetMachineState()
			if result := f.admit(pod); !result.Admit {
				t.Fatalf("admission = %+v", result)
			}
			for attempt := 0; attempt < 2; attempt++ {
				hook := &internalContainerLifecycleImpl{cpuManager: f.cpu, memoryManager: f.memory}
				for _, c := range pod.Spec.Containers {
					config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
					if err := hook.PreCreateContainer(f.logger, pod, &c, config); err != nil {
						t.Fatal(err)
					}
					if config.Linux.Resources.CpusetMems != "0" {
						t.Fatalf("memory mask = %s", config.Linux.Resources.CpusetMems)
					}
					for _, block := range f.memory.State().GetMemoryBlocks(string(pod.UID), c.Name) {
						if len(block.NUMAAffinity) != 1 || block.NUMAAffinity[0] != 0 || block.Size != numaHugepageUnits[block.Type] {
							t.Fatalf("memory block = %+v", block)
						}
					}
				}
				f.restart()
				if result := f.admit(pod); !result.Admit {
					t.Fatalf("recovery = %+v", result)
				}
			}
			for _, c := range pod.Spec.Containers {
				f.remove(pod, c)
			}
			if !f.cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(f.memory.State().GetMachineState(), beforeMemory) {
				t.Fatal("cleanup leaked resources")
			}
		})
	}
}

func TestIndependentNUMAHugepageInitCoverageRecovery(t *testing.T) {
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for name := range numaHugepageUnits {
			for _, recovery := range []string{"corrupt init", "missing applications", "application restart policy"} {
				missing := recovery != "corrupt init"
				t.Run(form+"/"+string(name)+"/"+recovery, func(t *testing.T) {
					f := newPodWideNUMAFixtureWithHugepages(t, numaHugepageCapacity())
					pod := podWidePod(form)
					pod.Spec.InitContainers = []v1.Container{podWideContainer("init", 3, false)}
					if recovery == "application restart policy" {
						pod.Spec.Containers[0].RestartPolicy = ptr.To(v1.ContainerRestartPolicyAlways)
					}
					addNUMAHugepages(pod)
					if result := f.admit(pod); !result.Admit {
						t.Fatalf("admission = %+v", result)
					}
					checkpoint, err := memorystate.NewCheckpointState(f.logger, f.directory, "memory_manager_state", "Static")
					if err != nil {
						t.Fatal(err)
					}
					machine := checkpoint.GetMachineState()
					if missing {
						for _, c := range pod.Spec.Containers {
							for _, b := range checkpoint.GetMemoryBlocks(string(pod.UID), c.Name) {
								machine[3].NumberOfAssignments--
								machine[3].MemoryMap[b.Type].Reserved -= b.Size
								machine[3].MemoryMap[b.Type].Free += b.Size
							}
							checkpoint.Delete(string(pod.UID), c.Name)
						}
					} else {
						blocks := checkpoint.GetMemoryBlocks(string(pod.UID), "init")
						for i, b := range blocks {
							if b.Type == name {
								machine[3].MemoryMap[name].Reserved -= b.Size
								machine[3].MemoryMap[name].Free += b.Size
								blocks[i].Size = 0
							}
						}
						checkpoint.SetMemoryBlocks(string(pod.UID), "init", blocks)
					}
					checkpoint.SetMachineState(machine)
					f.restart()
					before := snapshotNUMAHugepages(f)
					if !missing {
						if result := f.admit(pod); result.Admit {
							t.Fatal("under-reserved init memory was admitted")
						}
						before.assertUnchanged(f)
						hook := &internalContainerLifecycleImpl{cpuManager: f.cpu, memoryManager: f.memory}
						config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
						if err := hook.PreCreateContainer(f.logger, pod, &pod.Spec.InitContainers[0], config); err == nil {
							t.Fatal("under-reserved init memory reached runtime")
						}
						return
					}
					f.scope.AddHintProvider(f.logger, podWideFailure{container: "app-b"})
					for attempt := 0; attempt < 2; attempt++ {
						if result := f.admit(pod); result.Admit || !strings.Contains(result.Message, "injected failure") {
							t.Fatalf("partial failure = %+v", result)
						}
						before.assertUnchanged(f)
					}
					f.restart()
					for attempt := 0; attempt < 2; attempt++ {
						if result := f.admit(pod); !result.Admit {
							t.Fatalf("partial recovery = %+v", result)
						}
						checkNUMAMemoryPeak(t, f, 3)
						f.masks(pod, append(pod.Spec.InitContainers, pod.Spec.Containers...))
						f.restart()
					}
				})
			}
		}
	}
}
