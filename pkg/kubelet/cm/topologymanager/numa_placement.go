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

package topologymanager

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	corehelper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
)

// NUMAPlacement keeps the resolved resource targets separate from participation.
// Node zero is a valid target, so it cannot also signal an ordinary Pod.
type NUMAPlacement struct {
	Required    bool
	CPU         int
	Memory      int
	Independent bool
}

// String names the Pod fields so a refusal points at what the user set.
// Legacy Pods keep their established "NUMA node N" wording.
func (p NUMAPlacement) String() string {
	if !p.Independent {
		return fmt.Sprintf("NUMA node %d", p.CPU)
	}
	return fmt.Sprintf("NUMA placement cpuNUMA=%s, memNUMA=%s", numaTargetValue(p.CPU), numaTargetValue(p.Memory))
}

// CPUTarget and MemoryTarget describe one side before automatic selection
// resolves it, so an unresolved side never surfaces as node -1.
func (p NUMAPlacement) CPUTarget() string { return numaTarget("CPU", p.CPU) }

func (p NUMAPlacement) MemoryTarget() string { return numaTarget("memory", p.Memory) }

func numaTarget(resource string, id int) string {
	if id == -1 {
		return "automatic " + resource + " NUMA placement"
	}
	return fmt.Sprintf("requested NUMA node %d", id)
}

func numaTargetValue(id int) string {
	if id == -1 {
		return "automatic"
	}
	return fmt.Sprint(id)
}

// CheckPodNUMANodes checks surviving assignments, including init allocations
// whose resources have been reused. Missing assignments do not choose a target.
func CheckPodNUMANodes(pod *v1.Pod, resource string, target int, assignedNodes func(*v1.Container) sets.Set[int]) error {
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		nodes := assignedNodes(&container)
		if nodes.Len() == 0 {
			continue
		}
		if nodes.Len() != 1 || target != -1 && !nodes.Has(target) {
			return fmt.Errorf("%s assignment conflicts with Pod-wide singleton NUMA placement (target %s): container %s uses nodes %v", resource, numaTargetValue(target), container.Name, sets.List(nodes))
		}
		target = sets.List(nodes)[0]
	}
	return nil
}

// NUMAContainersOverlap follows sequential init and restartable sidecar
// lifetimes, including assignments preallocated for containers not yet started.
func NUMAContainersOverlap(pod *v1.Pod, first, second *v1.Container) bool {
	position := func(container *v1.Container) (int, bool) {
		for index, init := range pod.Spec.InitContainers {
			if init.Name == container.Name {
				return index, !podutil.IsRestartableInitContainer(&init)
			}
		}
		return len(pod.Spec.InitContainers), false
	}
	firstIndex, firstRegular := position(first)
	secondIndex, secondRegular := position(second)
	if firstRegular && secondRegular {
		return false
	}
	if firstRegular {
		return secondIndex < firstIndex
	}
	if secondRegular {
		return firstIndex < secondIndex
	}
	return true
}

// ResolveNUMAPlacement keeps omitted independent targets unresolved. Admission
// resolves automatic targets before allocation.
func ResolveNUMAPlacement(pod *v1.Pod) NUMAPlacement {
	if pod.Spec.CPUNUMA != nil || pod.Spec.MemNUMA != nil {
		placement := NUMAPlacement{Required: true, Independent: true, CPU: -1, Memory: -1}
		if pod.Spec.CPUNUMA != nil {
			placement.CPU = int(*pod.Spec.CPUNUMA)
		}
		if pod.Spec.MemNUMA != nil {
			placement.Memory = int(*pod.Spec.MemNUMA)
		}
		return placement
	}
	if pod.Spec.NUMANode == nil {
		return NUMAPlacement{}
	}
	id := int(*pod.Spec.NUMANode)
	return NUMAPlacement{Required: true, CPU: id, Memory: id}
}

// ValidateIndependentNUMARequest also protects static Pods, which can bypass
// API-server validation, and callers restoring allocations before container start.
func ValidateIndependentNUMARequest(pod *v1.Pod) error {
	placement := ResolveNUMAPlacement(pod)
	if !placement.Independent {
		return nil
	}
	if pod.Spec.NUMANode != nil {
		return fmt.Errorf("numaNode cannot be combined with cpuNUMA or memNUMA")
	}
	if pod.Spec.CPUNUMA != nil && (placement.CPU < 0 || placement.CPU > 63) || pod.Spec.MemNUMA != nil && (placement.Memory < 0 || placement.Memory > 63) {
		return fmt.Errorf("cpuNUMA and memNUMA must be between 0 and 63")
	}
	if len(pod.Spec.Containers) == 0 {
		return fmt.Errorf("NUMA placement requires at least one application container")
	}
	if pod.Spec.Resources != nil {
		return fmt.Errorf("pod-level resources are not supported with NUMA placement")
	}
	if len(pod.Spec.EphemeralContainers) != 0 {
		return fmt.Errorf("ephemeral containers are not supported with NUMA placement")
	}
	if pod.Spec.OS != nil && pod.Spec.OS.Name != v1.Linux {
		return fmt.Errorf("independent NUMA placement requires a Linux Pod")
	}
	if v1qos.ComputePodQOS(pod) != v1.PodQOSGuaranteed {
		return fmt.Errorf("NUMA placement requires Guaranteed equal CPU and memory requests and limits")
	}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		cpu := container.Resources.Requests[v1.ResourceCPU]
		_, isWhole := cpu.AsScale(0)
		if !isWhole || cpu.Sign() <= 0 {
			return fmt.Errorf("container %s: NUMA placement requires a positive whole number of CPUs", container.Name)
		}
		for _, resources := range []v1.ResourceList{container.Resources.Requests, container.Resources.Limits} {
			for name := range resources {
				if corehelper.IsHugePageResourceName(name) {
					request, limit := container.Resources.Requests[name], container.Resources.Limits[name]
					if request.Cmp(limit) != 0 {
						return fmt.Errorf("container %s: NUMA placement requires equal requests and limits for %s", container.Name, name)
					}
				}
			}
		}
	}
	return nil
}
