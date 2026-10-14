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
	"strings"

	v1 "k8s.io/api/core/v1"
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
	if pod.Spec.MemNUMA == nil {
		return fmt.Errorf("unsupported independent-numa slice: memNUMA is required; CPU-only partial requests are not implemented")
	}
	if pod.Spec.CPUNUMA != nil && (placement.CPU < 0 || placement.CPU > 63) || placement.Memory < 0 || placement.Memory > 63 {
		return fmt.Errorf("cpuNUMA and memNUMA must be between 0 and 63")
	}
	if len(pod.Spec.Containers) != 1 || len(pod.Spec.InitContainers) != 0 {
		return fmt.Errorf("unsupported independent-numa slice: exactly one application container and no init or sidecar containers are supported")
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
	if v1qos.GetPodQOS(pod) != v1.PodQOSGuaranteed {
		return fmt.Errorf("NUMA placement requires Guaranteed equal CPU and memory requests and limits")
	}
	container := &pod.Spec.Containers[0]
	cpu := container.Resources.Requests[v1.ResourceCPU]
	_, isWhole := cpu.AsScale(0)
	if !isWhole || cpu.Sign() <= 0 {
		return fmt.Errorf("NUMA placement requires a positive whole number of CPUs")
	}
	for _, resources := range []v1.ResourceList{container.Resources.Requests, container.Resources.Limits} {
		for name := range resources {
			if strings.HasPrefix(string(name), v1.ResourceHugePagesPrefix) {
				return fmt.Errorf("unsupported independent-numa slice: hugepage requests are not implemented (%s)", name)
			}
		}
	}
	return nil
}
