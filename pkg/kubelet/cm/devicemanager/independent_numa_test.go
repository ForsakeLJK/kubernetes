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
	"reflect"
	"strings"
	"testing"
	"time"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type independentNUMAFailure struct{ topologymanager.HintProvider }

func (independentNUMAFailure) Allocate(context.Context, *v1.Pod, *v1.Container, lifecycle.Operation) error {
	return errors.New("injected final allocation failure")
}

func TestIndependentNUMADeviceAllocation(t *testing.T) {
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
			t.Run(name, func(t *testing.T) {
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
				if tc.pluginFailure {
					device.endpoints["domain1.com/resource1"] = endpointInfo{e: &MockEndpoint{allocateFunc: func([]string) (*pluginapi.AllocateResponse, error) {
						return nil, errors.New("injected plugin allocation failure")
					}}}
				}
				scope.AddHintProvider(logger, cpu)
				scope.AddHintProvider(logger, memory)
				scope.AddHintProvider(logger, device)
				resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi"), "domain1.com/resource1": resource.MustParse("2")}
				pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "independent-device"}, Spec: v1.PodSpec{CPUNUMA: ptr.To[int32](2), MemNUMA: ptr.To[int32](3), Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: resources, Limits: resources}}}}}
				if automatic == "CPU" {
					pod.Spec.CPUNUMA = nil
				} else if automatic == "memory" || automatic == "remote memory" {
					pod.Spec.MemNUMA = nil
				}
				control := pod.DeepCopy()
				control.UID = "unrelated-pod"
				delete(control.Spec.Containers[0].Resources.Requests, "domain1.com/resource1")
				delete(control.Spec.Containers[0].Resources.Limits, "domain1.com/resource1")
				if result := scope.Admit(ctx, control, lifecycle.AddOperation); !result.Admit {
					t.Fatalf("control admission = %+v", result)
				}
				if tc.finalFailure {
					scope.AddHintProvider(logger, independentNUMAFailure{HintProvider: memory})
				}
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
						continue
					}
					if !result.Admit {
						t.Fatalf("admission = %+v", result)
					}
					memoryNode := 3
					if automatic == "memory" {
						memoryNode = 2
					}
					if !cpu.GetCPUAffinity(string(pod.UID), "app").Equals(cpuset.New(2)) || !memory.GetMemoryNUMANodes(logger, pod, &pod.Spec.Containers[0]).Has(memoryNode) || len(device.GetDevices(string(pod.UID), "app")["domain1.com/resource1"]) != 2 {
						t.Fatal("concrete device/CPU/memory placement did not match")
					}
				}
			})
		}
	}
}
