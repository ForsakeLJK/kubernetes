//go:build linux

/*
Copyright 2021 The Kubernetes Authors.

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
	"fmt"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/klog/v2"
)

func (i *internalContainerLifecycleImpl) PreCreateContainer(logger klog.Logger, pod *v1.Pod, container *v1.Container, containerConfig *runtimeapi.ContainerConfig) error {
	if pod.Spec.NUMANode != nil {
		if i.cpuManager == nil || i.memoryManager == nil {
			return fmt.Errorf("NUMA node %d requires CPU and memory managers", *pod.Spec.NUMANode)
		}
		cpuValidator, cpuOK := i.cpuManager.(interface {
			ValidateNUMAPlacement(*v1.Pod, *v1.Container) error
		})
		memoryValidator, memoryOK := i.memoryManager.(interface {
			ValidateNUMAPlacement(*v1.Pod, *v1.Container) error
		})
		if !cpuOK || !memoryOK {
			return fmt.Errorf("NUMA node %d requires CPU and memory placement validation", *pod.Spec.NUMANode)
		}
		if err := cpuValidator.ValidateNUMAPlacement(pod, container); err != nil {
			return err
		}
		if err := memoryValidator.ValidateNUMAPlacement(pod, container); err != nil {
			return err
		}
	}
	if i.cpuManager != nil {
		allocatedCPUs := i.cpuManager.GetCPUAffinity(string(pod.UID), container.Name)
		if !allocatedCPUs.IsEmpty() {
			containerConfig.Linux.Resources.CpusetCpus = allocatedCPUs.String()
		}
	}

	if i.memoryManager != nil {
		numaNodes := i.memoryManager.GetMemoryNUMANodes(logger, pod, container)
		if numaNodes.Len() > 0 {
			var affinity []string
			for _, numaNode := range sets.List(numaNodes) {
				affinity = append(affinity, strconv.Itoa(numaNode))
			}
			containerConfig.Linux.Resources.CpusetMems = strings.Join(affinity, ",")
		}
	}

	return nil
}
