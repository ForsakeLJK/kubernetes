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

package kubelet

import (
	"context"
	"fmt"
	"runtime"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type numaPlacementAdmitHandler struct {
	goos           string
	config         cm.NodeConfig
	getMachineInfo func() (*cadvisorapi.MachineInfo, error)
}

func newNUMAPlacementAdmitHandler(config cm.NodeConfig, getMachineInfo func() (*cadvisorapi.MachineInfo, error)) lifecycle.PodAdmitHandler {
	return &numaPlacementAdmitHandler{goos: runtime.GOOS, config: config, getMachineInfo: getMachineInfo}
}

func (h *numaPlacementAdmitHandler) Admit(_ context.Context, attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	if attrs.Pod.Spec.NUMANode == nil {
		return lifecycle.PodAdmitResult{Admit: true}
	}
	id := *attrs.Pod.Spec.NUMANode
	if h.goos != "linux" || h.config.CPUManagerPolicy != "static" || h.config.MemoryManagerPolicy != "Static" ||
		h.config.TopologyManagerPolicy != "single-numa-node" || h.config.TopologyManagerScope != "pod" {
		return lifecycle.PodAdmitResult{
			Reason:  "NUMAPlacementUnsupported",
			Message: fmt.Sprintf("NUMA node %d requires Linux, CPU Manager static, Memory Manager Static, and Topology Manager single-numa-node with pod scope", id),
		}
	}
	info, err := h.getMachineInfo()
	if err != nil {
		return lifecycle.PodAdmitResult{
			Reason:  "NUMAPlacementUnsupported",
			Message: fmt.Sprintf("cannot verify NUMA node %d against local machine topology: %v", id, err),
		}
	}
	if info == nil {
		return lifecycle.PodAdmitResult{
			Reason:  "NUMAPlacementUnsupported",
			Message: fmt.Sprintf("cannot verify NUMA node %d: local machine topology information is unavailable", id),
		}
	}
	for _, node := range info.Topology {
		if node.Id == int(id) {
			if supportedNUMAPlacementPod(attrs.Pod) {
				return lifecycle.PodAdmitResult{Admit: true}
			}
			return lifecycle.PodAdmitResult{
				Reason:  "NUMAPlacementNotImplemented",
				Message: fmt.Sprintf("NUMA node %d: explicit placement for hugepages is not yet supported", id),
			}
		}
	}
	return lifecycle.PodAdmitResult{
		Reason:  "NUMANodeNotFound",
		Message: fmt.Sprintf("NUMA node %d is absent from the local machine topology", id),
	}
}

func supportedNUMAPlacementPod(pod *v1.Pod) bool {
	if len(pod.Spec.Containers) == 0 {
		return false
	}
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		for resourceName := range container.Resources.Requests {
			if v1helper.IsHugePageResourceName(resourceName) {
				return false
			}
		}
		for resourceName := range container.Resources.Limits {
			if v1helper.IsHugePageResourceName(resourceName) {
				return false
			}
		}
	}
	return true
}
