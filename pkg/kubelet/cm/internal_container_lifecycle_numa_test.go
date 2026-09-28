//go:build linux

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

package cm

import (
	"errors"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	cpustate "k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/memorymanager"
	memorystate "k8s.io/kubernetes/pkg/kubelet/cm/memorymanager/state"
	"k8s.io/utils/cpuset"
)

type numaRuntimeCPU struct {
	cpumanager.Manager
	err  error
	cpus cpuset.CPUSet
}

func (m numaRuntimeCPU) ValidateNUMAPlacement(*v1.Pod, *v1.Container) error { return m.err }
func (m numaRuntimeCPU) GetCPUAffinity(string, string) cpuset.CPUSet {
	if !m.cpus.IsEmpty() {
		return m.cpus
	}
	return cpuset.New(4, 6)
}

type numaRuntimeMemory struct {
	memorymanager.Manager
	err   error
	nodes sets.Set[int]
}

func (m numaRuntimeMemory) ValidateNUMAPlacement(*v1.Pod, *v1.Container) error { return m.err }
func (m numaRuntimeMemory) GetMemoryNUMANodes(klog.Logger, *v1.Pod, *v1.Container) sets.Set[int] {
	if m.nodes.Len() > 0 {
		return m.nodes
	}
	return sets.New(2)
}

func TestExplicitNUMARuntimeMasksFromCheckpoint(t *testing.T) {
	logger := klog.Background()
	restart := v1.ContainerRestartPolicyAlways
	node := int32(2)
	pod := &v1.Pod{Spec: v1.PodSpec{NUMANode: &node,
		InitContainers: []v1.Container{{Name: "init"}, {Name: "sidecar", RestartPolicy: &restart}},
		Containers:     []v1.Container{{Name: "app"}},
	}}
	pod.UID = "restored-runtime-pod"
	cpuDirectory, memoryDirectory := t.TempDir(), t.TempDir()
	cpuCheckpoint, err := cpustate.NewCheckpointState(logger, cpuDirectory, "numa-runtime-cpu", "static", containermap.NewContainerMap())
	if err != nil {
		t.Fatal(err)
	}
	cpuCheckpoint.SetDefaultCPUSet(cpuset.New(0, 1, 2, 3))
	memoryCheckpoint, err := memorystate.NewCheckpointState(logger, memoryDirectory, "numa-runtime-memory", "Static")
	if err != nil {
		t.Fatal(err)
	}
	for index, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		cpuCheckpoint.SetCPUSet(string(pod.UID), container.Name, cpuset.New(4+index*4, 6+index*4))
		memoryCheckpoint.SetMemoryBlocks(string(pod.UID), container.Name, []memorystate.Block{{NUMAAffinity: []int{2}, Type: v1.ResourceMemory, Size: 1024}})
	}
	cpuRestored, err := cpustate.NewCheckpointState(logger, cpuDirectory, "numa-runtime-cpu", "static", containermap.NewContainerMap())
	if err != nil {
		t.Fatal(err)
	}
	memoryRestored, err := memorystate.NewCheckpointState(logger, memoryDirectory, "numa-runtime-memory", "Static")
	if err != nil {
		t.Fatal(err)
	}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		cpus, exists := cpuRestored.GetCPUSet(string(pod.UID), container.Name)
		blocks := memoryRestored.GetMemoryBlocks(string(pod.UID), container.Name)
		if !exists || len(blocks) != 1 {
			t.Fatalf("%s assignment was not restored", container.Name)
		}
		lifecycle := &internalContainerLifecycleImpl{
			cpuManager:    numaRuntimeCPU{cpus: cpus},
			memoryManager: numaRuntimeMemory{nodes: sets.New(blocks[0].NUMAAffinity...)},
		}
		config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
		if err := lifecycle.PreCreateContainer(logger, pod, &container, config); err != nil {
			t.Fatalf("%s restart: %v", container.Name, err)
		}
		if config.Linux.Resources.CpusetCpus != cpus.String() || config.Linux.Resources.CpusetMems != "2" {
			t.Fatalf("%s restored runtime masks = CPUs %q, memory %q", container.Name, config.Linux.Resources.CpusetCpus, config.Linux.Resources.CpusetMems)
		}
	}
}

func TestExplicitNUMARuntimeMasks(t *testing.T) {
	node := int32(2)
	pod := &v1.Pod{Spec: v1.PodSpec{NUMANode: &node}}
	container := &v1.Container{Name: "app"}
	for _, tc := range []struct {
		name      string
		cpuErr    error
		memErr    error
		wantErr   bool
		omitted   bool
		hugepages bool
	}{
		{name: "successful placement"},
		{name: "hugepage placement", hugepages: true},
		{name: "missing CPU assignment", cpuErr: errors.New("missing CPU assignment"), wantErr: true},
		{name: "missing memory assignment", memErr: errors.New("missing memory assignment"), wantErr: true},
		{name: "omitted field retains runtime mask behavior", cpuErr: errors.New("unused validator"), omitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle := &internalContainerLifecycleImpl{cpuManager: numaRuntimeCPU{err: tc.cpuErr}, memoryManager: numaRuntimeMemory{err: tc.memErr}}
			config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
			casePod := pod.DeepCopy()
			caseContainer := container.DeepCopy()
			if tc.hugepages {
				pages := v1.ResourceName(v1.ResourceHugePagesPrefix + "2Mi")
				caseContainer.Resources.Requests = v1.ResourceList{pages: resource.MustParse("4Mi")}
				caseContainer.Resources.Limits = v1.ResourceList{pages: resource.MustParse("4Mi")}
			}
			if tc.omitted {
				casePod.Spec.NUMANode = nil
			}
			err := lifecycle.PreCreateContainer(klog.Background(), casePod, caseContainer, config)
			if (err != nil) != tc.wantErr {
				t.Fatalf("PreCreateContainer error = %v", err)
			}
			if !tc.wantErr && (config.Linux.Resources.CpusetCpus != "4,6" || config.Linux.Resources.CpusetMems != "2") {
				t.Fatalf("runtime masks = CPUs %q, memory %q", config.Linux.Resources.CpusetCpus, config.Linux.Resources.CpusetMems)
			}
		})
	}
}

func TestExplicitNUMARuntimeMasksForEveryPodContainer(t *testing.T) {
	node := int32(2)
	restart := v1.ContainerRestartPolicyAlways
	pod := &v1.Pod{Spec: v1.PodSpec{
		NUMANode: &node,
		InitContainers: []v1.Container{
			{Name: "init"},
			{Name: "sidecar", RestartPolicy: &restart},
		},
		Containers: []v1.Container{{Name: "app-a"}, {Name: "app-b"}},
	}}
	lifecycle := &internalContainerLifecycleImpl{cpuManager: numaRuntimeCPU{}, memoryManager: numaRuntimeMemory{}}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		config := &runtimeapi.ContainerConfig{Linux: &runtimeapi.LinuxContainerConfig{Resources: &runtimeapi.LinuxContainerResources{}}}
		if err := lifecycle.PreCreateContainer(klog.Background(), pod, &container, config); err != nil {
			t.Fatalf("%s: %v", container.Name, err)
		}
		if config.Linux.Resources.CpusetCpus != "4,6" || config.Linux.Resources.CpusetMems != "2" {
			t.Fatalf("%s runtime masks = CPUs %q, memory %q", container.Name, config.Linux.Resources.CpusetCpus, config.Linux.Resources.CpusetMems)
		}
	}
}
