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

package devicemanager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	"google.golang.org/protobuf/proto"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/devicemanager/checkpoint"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/test/utils/ktesting"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
)

type independentNUMASourcesNotReady struct{}

func (independentNUMASourcesNotReady) AddSource(string) {}
func (independentNUMASourcesNotReady) AllReady() bool   { return false }

type independentNUMAFailure struct {
	topologymanager.HintProvider
	container string
	enabled   *bool
}

func (p independentNUMAFailure) Allocate(_ context.Context, _ *v1.Pod, c *v1.Container, _ lifecycle.Operation) error {
	if (p.enabled == nil || *p.enabled) && (p.container == "" || c.Name == p.container) {
		return errors.New("injected final allocation failure")
	}
	return nil
}

func TestIndependentNUMADeviceAllocation(t *testing.T) {
	t.Run("ordinary memory", func(t *testing.T) { testIndependentNUMADeviceAllocation(t, false) })
	t.Run("hugepages", func(t *testing.T) { testIndependentNUMADeviceAllocation(t, true) })
}

func testIndependentNUMADeviceAllocation(t *testing.T, hugepages bool) {
	for _, workload := range []string{"single", "applications", "init", "sidecar"} {
		for _, automatic := range []string{"", "CPU", "memory", "remote memory"} {
			for _, tc := range []struct {
				name                        string
				devices                     checkpoint.DevicesPerNUMA
				topology                    bool
				pluginFailure, finalFailure bool
				want                        string
			}{
				{name: "CPU anchored device", topology: true, devices: checkpoint.DevicesPerNUMA{2: {"dev1", "dev2"}}},
				{name: "topology unaware device", devices: checkpoint.DevicesPerNUMA{2: {"dev1", "dev2"}}},
				{name: "device on memory target conflicts", topology: true, devices: checkpoint.DevicesPerNUMA{3: {"dev1", "dev2"}}, want: "Topology"},
				{name: "nonpreferred CPU hint", topology: true, devices: checkpoint.DevicesPerNUMA{0: {"dev1"}, 2: {"dev2"}}, want: "Topology"},
				{name: "plugin fails after CPU and memory", topology: true, devices: checkpoint.DevicesPerNUMA{2: {"dev1", "dev2"}}, pluginFailure: true, want: "plugin allocation failure"},
				{name: "failure after all three managers", topology: true, devices: checkpoint.DevicesPerNUMA{2: {"dev1", "dev2"}}, finalFailure: true, want: "final allocation failure"},
			} {
				name := tc.name
				if automatic != "" {
					name += " automatic " + automatic
					if automatic == "CPU" && tc.want == "Topology" {
						tc.want = "no feasible singleton CPU"
					}
				}
				t.Run(workload+"/"+name, func(t *testing.T) {
					logger, ctx := ktesting.NewTestContext(t)
					const gib = uint64(1024 * 1024 * 1024)
					info := &cadvisorapi.MachineInfo{NumCores: 5, Topology: []cadvisorapi.Node{
						{Id: 0, Memory: 8 * gib, Cores: []cadvisorapi.Core{{Id: 0, SocketID: 0, Threads: []int{0}}}},
						{Id: 2, Memory: 8 * gib, Cores: []cadvisorapi.Core{{Id: 1, SocketID: 1, Threads: []int{1}}, {Id: 2, SocketID: 1, Threads: []int{2}}, {Id: 3, SocketID: 1, Threads: []int{3}}, {Id: 4, SocketID: 1, Threads: []int{4}}}},
						{Id: 3, Memory: 8 * gib},
					}}
					numaInfo := &topologymanager.NUMAInfo{Nodes: []int{0, 2, 3}}
					if automatic == "remote memory" {
						info.Topology[1].Memory = gib / 2
						numaInfo.NUMADistances = map[int][]uint64{2: {40, 10, 20}}
					}
					if hugepages {
						for i := range info.Topology {
							info.Topology[i].HugePages = []cadvisorapi.HugePagesInfo{{PageSize: 2048, NumPages: 8}, {PageSize: 1048576, NumPages: 4}}
							info.Topology[i].Memory += 4*gib + 16*1024*1024
						}
					}
					scope := topologymanager.NewPodScope(topologymanager.NewIndependentNUMAPolicy(numaInfo, topologymanager.PolicyOptions{}))
					directory := t.TempDir()
					reservation := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")}
					cpu, err := cpumanager.NewManager(logger, "static", nil, time.Hour, info, cpuset.New(0), reservation, directory, scope)
					if err != nil {
						t.Fatal(err)
					}
					memory, err := memorymanager.NewManager(logger, "Static", info, reservation, []kubeletconfig.MemoryReservation{{NumaNode: 0, Limits: v1.ResourceList{v1.ResourceMemory: resource.MustParse("1Gi")}}}, directory, scope)
					if err != nil {
						t.Fatal(err)
					}
					if err := cpu.Start(ctx, func() []*v1.Pod { return nil }, independentNUMASourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
						t.Fatal(err)
					}
					if err := memory.Start(ctx, func() []*v1.Pod { return nil }, independentNUMASourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
						t.Fatal(err)
					}
					device, err := getTestManager(logger, t.TempDir(), func() []*v1.Pod { return nil }, []TestResource{{resourceName: "domain1.com/resource1", devs: tc.devices, topology: tc.topology}})
					if err != nil {
						t.Fatal(err)
					}
					device.topologyAffinityStore, device.numaNodes = scope, []int{0, 2, 3}
					device.sourcesReady = independentNUMASourcesNotReady{}
					pluginCalls := 0
					if tc.pluginFailure {
						device.endpoints["domain1.com/resource1"] = endpointInfo{e: &MockEndpoint{allocateFunc: func([]string) (*pluginapi.AllocateResponse, error) {
							pluginCalls++
							if workload == "single" || pluginCalls%2 == 0 {
								return nil, errors.New("injected plugin allocation failure")
							}
							return &pluginapi.AllocateResponse{ContainerResponses: []*pluginapi.ContainerAllocateResponse{{}}}, nil
						}}}
					}
					scope.AddHintProvider(logger, cpu)
					scope.AddHintProvider(logger, memory)
					scope.AddHintProvider(logger, device)
					resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi"), "domain1.com/resource1": resource.MustParse("2")}
					if hugepages {
						resources["hugepages-2Mi"] = resource.MustParse("2Mi")
						resources["hugepages-1Gi"] = resource.MustParse("1Gi")
					}
					pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "independent-device"}, Spec: v1.PodSpec{CPUNUMA: ptr.To[int32](2), MemNUMA: ptr.To[int32](3), Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: resources, Limits: resources}}}}}
					if workload != "single" {
						pod.Spec.Containers[0].Resources.Requests["domain1.com/resource1"] = resource.MustParse("1")
						pod.Spec.Containers[0].Resources.Limits["domain1.com/resource1"] = resource.MustParse("1")
						second := pod.Spec.Containers[0].DeepCopy()
						second.Name = "second"
						if workload == "sidecar" {
							second.RestartPolicy = ptr.To(v1.ContainerRestartPolicyAlways)
							pod.Spec.InitContainers = []v1.Container{*second}
						} else {
							pod.Spec.Containers = append(pod.Spec.Containers, *second)
							if workload == "init" {
								init := second.DeepCopy()
								init.Name = "init"
								init.Resources.Requests["domain1.com/resource1"] = resource.MustParse("2")
								init.Resources.Limits["domain1.com/resource1"] = resource.MustParse("2")
								pod.Spec.InitContainers = []v1.Container{*init}
							}
						}
					}
					if automatic == "CPU" {
						pod.Spec.CPUNUMA = nil
					} else if automatic == "memory" || automatic == "remote memory" {
						pod.Spec.MemNUMA = nil
					}
					control := pod.DeepCopy()
					control.UID = "unrelated-pod"
					for i := range control.Spec.Containers {
						delete(control.Spec.Containers[i].Resources.Requests, "domain1.com/resource1")
						delete(control.Spec.Containers[i].Resources.Limits, "domain1.com/resource1")
					}
					control.Spec.InitContainers = nil
					if result := scope.Admit(ctx, control, lifecycle.AddOperation); !result.Admit {
						t.Fatalf("control admission = %+v", result)
					}
					if tc.finalFailure {
						last := "app"
						if workload == "applications" || workload == "init" {
							last = "second"
						}
						scope.AddHintProvider(logger, independentNUMAFailure{HintProvider: memory, container: last})
					}
					device.devicesToReuse["preserved-reuse"] = map[string]sets.Set[string]{"domain1.com/resource1": sets.New("unrelated-device")}
					beforeCPU, beforeMemory := cpu.State().GetDefaultCPUSet(), memory.State().GetMachineState()
					beforeCPUAssignments, beforeMemoryAssignments := cpu.State().GetCPUAssignments(), memory.State().GetMemoryAssignments()
					for attempt := 0; attempt < 3; attempt++ {
						result := scope.Admit(ctx, pod, lifecycle.AddOperation)
						if tc.want != "" {
							if result.Admit || !strings.Contains(result.Message, tc.want) {
								t.Fatalf("admission = %+v, want %s", result, tc.want)
							}
							if !cpu.State().GetDefaultCPUSet().Equals(beforeCPU) || !reflect.DeepEqual(memory.State().GetMachineState(), beforeMemory) || !reflect.DeepEqual(cpu.State().GetCPUAssignments(), beforeCPUAssignments) || !reflect.DeepEqual(memory.State().GetMemoryAssignments(), beforeMemoryAssignments) || len(device.GetDevices(string(pod.UID), "app")) != 0 {
								t.Fatal("failed admission leaked resources or changed unrelated assignments")
							}
							if !device.devicesToReuse["preserved-reuse"]["domain1.com/resource1"].Has("unrelated-device") || len(device.devicesToReuse[string(pod.UID)]) != 0 {
								t.Fatal("rollback changed pre-existing device reuse or retained new reuse")
							}
							for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
								if len(device.GetDevices(string(pod.UID), c.Name)) != 0 {
									t.Fatal("rollback retained an earlier container's devices")
								}
							}
							continue
						}
						if !result.Admit {
							t.Fatalf("admission = %+v", result)
						}
						memoryNode := 3
						if automatic == "memory" {
							memoryNode = 2
						}
						for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
							requestedDevices := c.Resources.Requests["domain1.com/resource1"]
							if !cpu.GetCPUAffinity(string(pod.UID), c.Name).IsSubsetOf(cpuset.New(1, 2, 3, 4)) || !memory.GetMemoryNUMANodes(logger, pod, &c).Has(memoryNode) || len(device.GetDevices(string(pod.UID), c.Name)["domain1.com/resource1"]) != int(requestedDevices.Value()) {
								t.Fatalf("%s: CPUs %s, memory %v, devices %v, requested %s", c.Name, cpu.GetCPUAffinity(string(pod.UID), c.Name), memory.GetMemoryNUMANodes(logger, pod, &c), device.GetDevices(string(pod.UID), c.Name), requestedDevices.String())
							}
						}
					}
				})
			}
		}
	}
}

func TestIndependentNUMAPartialDeviceRecovery(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, missingInit := range []bool{false, true} {
			if completed && missingInit {
				continue
			}
			for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
				t.Run(fmt.Sprintf("%s/completed-init-%t/missing-init-%t", form, completed, missingInit), func(t *testing.T) {
					logger, ctx := ktesting.NewTestContext(t)
					directory := t.TempDir()
					resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("256Mi"), "domain1.com/resource1": resource.MustParse("1")}
					container := func(name string) v1.Container {
						return v1.Container{Name: name, Resources: v1.ResourceRequirements{Requests: resources.DeepCopy(), Limits: resources.DeepCopy()}}
					}
					pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "partial-device"}, Spec: v1.PodSpec{CPUNUMA: ptr.To[int32](2), MemNUMA: ptr.To[int32](3), InitContainers: []v1.Container{container("init")}, Containers: []v1.Container{container("app-a"), container("app-b")}}}
					if form == "CPU-only" {
						pod.Spec.MemNUMA = nil
					}
					if form == "memory-only" {
						pod.Spec.CPUNUMA = nil
					}
					machine := &cadvisorapi.MachineInfo{NumCores: 5, Topology: []cadvisorapi.Node{
						{Id: 0, Memory: 2 << 30, Cores: []cadvisorapi.Core{{Id: 0, Threads: []int{0}}}},
						{Id: 2, Memory: 2 << 30, Cores: []cadvisorapi.Core{{Id: 1, SocketID: 1, Threads: []int{1, 2, 3, 4}}}},
						{Id: 3, Memory: 2 << 30},
					}}
					scope := topologymanager.NewPodScope(topologymanager.NewIndependentNUMAPolicy(&topologymanager.NUMAInfo{Nodes: []int{0, 2, 3}}, topologymanager.PolicyOptions{}))
					reservations := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")}
					cpu, err := cpumanager.NewManager(logger, "static", nil, time.Hour, machine, cpuset.New(0), reservations, directory, scope)
					if err != nil {
						t.Fatal(err)
					}
					memory, err := memorymanager.NewManager(logger, "Static", machine, reservations, []kubeletconfig.MemoryReservation{{NumaNode: 0, Limits: v1.ResourceList{v1.ResourceMemory: resource.MustParse("1Gi")}}}, directory, scope)
					if err != nil {
						t.Fatal(err)
					}
					if err := cpu.Start(ctx, func() []*v1.Pod { return nil }, independentNUMASourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
						t.Fatal(err)
					}
					if err := memory.Start(ctx, func() []*v1.Pod { return nil }, independentNUMASourcesNotReady{}, nil, nil, containermap.NewContainerMap()); err != nil {
						t.Fatal(err)
					}
					device, err := getTestManager(logger, t.TempDir(), func() []*v1.Pod { return []*v1.Pod{pod} }, []TestResource{{resourceName: "domain1.com/resource1", devs: checkpoint.DevicesPerNUMA{2: {"dev1", "dev2"}}, topology: true}})
					if err != nil {
						t.Fatal(err)
					}
					device.topologyAffinityStore, device.numaNodes = scope, []int{0, 2, 3}
					// Restore a released earlier app and a live sibling sharing the
					// sequential init's device, a valid checkpoint lifetime overlap.
					response, _ := proto.Marshal(&pluginapi.ContainerAllocateResponse{})
					if completed {
						pod.Spec.InitContainers[0].Resources.Requests["domain1.com/resource1"] = resource.MustParse("3")
						pod.Spec.InitContainers[0].Resources.Limits["domain1.com/resource1"] = resource.MustParse("3")
						pod.Status.InitContainerStatuses = []v1.ContainerStatus{{Name: "init", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}}}
					}
					entries := []checkpoint.PodDevicesEntry{
						{PodUID: string(pod.UID), ContainerName: "init", ResourceName: "domain1.com/resource1", DeviceIDs: checkpoint.DevicesPerNUMA{2: {"dev1"}}, AllocResp: response},
						{PodUID: string(pod.UID), ContainerName: "app-b", ResourceName: "domain1.com/resource1", DeviceIDs: checkpoint.DevicesPerNUMA{2: {"dev1"}}, AllocResp: response},
					}
					if completed {
						entries = entries[1:]
					}
					if missingInit {
						entries = []checkpoint.PodDevicesEntry{
							{PodUID: string(pod.UID), ContainerName: "app-a", ResourceName: "domain1.com/resource1", DeviceIDs: checkpoint.DevicesPerNUMA{2: {"dev2"}}, AllocResp: response},
							entries[1],
						}
					}
					data := checkpoint.New(entries, map[string][]string{"domain1.com/resource1": {"dev1", "dev2"}})
					if err := device.checkpointManager.CreateCheckpoint(kubeletDeviceManagerCheckpoint, data); err != nil {
						t.Fatal(err)
					}
					if err := device.readCheckpoint(logger); err != nil {
						t.Fatal(err)
					}
					device.healthyDevices["domain1.com/resource1"] = sets.New("dev1", "dev2")
					device.endpoints["domain1.com/resource1"] = endpointInfo{e: &MockEndpoint{allocateFunc: allocateStubFunc()}}
					scope.AddHintProvider(logger, cpu)
					scope.AddHintProvider(logger, memory)
					scope.AddHintProvider(logger, device)
					fail := true
					scope.AddHintProvider(logger, independentNUMAFailure{HintProvider: memory, container: "app-b", enabled: &fail})
					beforePool, beforeMemory := cpu.State().GetDefaultCPUSet(), memory.State().GetMachineState()
					for attempt := 0; attempt < 3; attempt++ {
						result := scope.Admit(ctx, pod, lifecycle.AddOperation)
						if result.Admit || !strings.Contains(result.Message, "final allocation failure") {
							t.Fatalf("partial device rollback = %+v", result)
						}
						if (len(device.GetDevices(string(pod.UID), "app-a")) != 0) != missingInit || device.GetDevices(string(pod.UID), "app-b")["domain1.com/resource1"]["dev1"] == nil || !cpu.State().GetDefaultCPUSet().Equals(beforePool) || !reflect.DeepEqual(memory.State().GetMachineState(), beforeMemory) {
							t.Fatal("partial rollback changed surviving devices or leaked capacity")
						}
					}
					fail = false
					if result := scope.Admit(ctx, pod, lifecycle.AddOperation); !result.Admit {
						t.Fatalf("partial device restoration = %+v", result)
					}
					a := device.GetDevices(string(pod.UID), "app-a")["domain1.com/resource1"]
					b := device.GetDevices(string(pod.UID), "app-b")["domain1.com/resource1"]
					if len(a) != 1 || len(b) != 1 || a["dev1"] != nil || b["dev1"] == nil {
						t.Fatalf("partial recovery shared a device between applications: %v / %v", a, b)
					}
				})
			}
		}
	}
}
