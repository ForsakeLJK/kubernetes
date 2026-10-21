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

package kubelet

import (
	"context"
	"fmt"
	"runtime"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
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
	placement := topologymanager.ResolveNUMAPlacement(attrs.Pod)
	if !placement.Required {
		return lifecycle.PodAdmitResult{Admit: true}
	}
	if err := topologymanager.ValidateIndependentNUMARequest(attrs.Pod); err != nil {
		return lifecycle.PodAdmitResult{Reason: "NUMAPlacementUnsupported", Message: err.Error()}
	}
	policySupported := h.config.TopologyManagerPolicy == topologymanager.PolicySingleNumaNode || h.config.TopologyManagerPolicy == topologymanager.PolicyIndependentNUMA
	policyNames := "single-numa-node or independent-numa"
	if placement.Independent {
		policySupported = h.config.TopologyManagerPolicy == topologymanager.PolicyIndependentNUMA
		policyNames = "independent-numa"
	}
	if h.goos != "linux" || h.config.CPUManagerPolicy != "static" || h.config.MemoryManagerPolicy != "Static" ||
		!policySupported || h.config.TopologyManagerScope != "pod" {
		return lifecycle.PodAdmitResult{
			Reason:  "NUMAPlacementUnsupported",
			Message: fmt.Sprintf("%s requires Linux, CPU Manager static, Memory Manager Static, and Topology Manager %s with pod scope", placement, policyNames),
		}
	}
	info, err := h.getMachineInfo()
	if err != nil {
		return lifecycle.PodAdmitResult{
			Reason:  "NUMAPlacementUnsupported",
			Message: fmt.Sprintf("cannot verify %s against local machine topology: %v", placement, err),
		}
	}
	if info == nil {
		return lifecycle.PodAdmitResult{
			Reason:  "NUMAPlacementUnsupported",
			Message: fmt.Sprintf("cannot verify %s: local machine topology information is unavailable", placement),
		}
	}
	for _, target := range []struct {
		resource string
		id       int
	}{{"CPU", placement.CPU}, {"memory", placement.Memory}} {
		if target.id == -1 {
			continue
		}
		found, hasCPUs := false, false
		for _, node := range info.Topology {
			if node.Id == target.id {
				found = true
				for _, core := range node.Cores {
					hasCPUs = hasCPUs || len(core.Threads) > 0
				}
			}
		}
		if !found {
			return lifecycle.PodAdmitResult{Reason: "NUMANodeNotFound", Message: fmt.Sprintf("%s NUMA node %d is absent from the local machine topology", target.resource, target.id)}
		}
		if placement.Independent && target.resource == "CPU" && !hasCPUs {
			return lifecycle.PodAdmitResult{Reason: "NUMAPlacementFailed", Message: fmt.Sprintf("CPU NUMA node %d contains no eligible CPUs", target.id)}
		}
	}
	return lifecycle.PodAdmitResult{Admit: true}
}
