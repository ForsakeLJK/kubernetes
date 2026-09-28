/*
Copyright 2020 The Kubernetes Authors.

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
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	resourcehelper "k8s.io/component-helpers/resource"
	"k8s.io/klog/v2"
	corehelper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/cm/admission"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
)

type podScope struct {
	scope
}

// Ensure podScope implements Scope interface
var _ Scope = &podScope{}

// NewPodScope returns a pod scope.
func NewPodScope(policy Policy) Scope {
	return &podScope{
		scope{
			name:             PodTopologyScope,
			podTopologyHints: podTopologyHints{},
			policy:           policy,
			podMap:           containermap.NewContainerMap(),
		},
	}
}

func (s *podScope) Admit(ctx context.Context, pod *v1.Pod, operation lifecycle.Operation) lifecycle.PodAdmitResult {
	// If the PodLevelResourceManagers feature is enabled, delegate the resource
	// allocation to the hint providers (CPU and Memory managers) via the AllocatePod call.
	// This is a one-time allocation for the entire pod.
	if utilfeature.DefaultFeatureGate.Enabled(features.PodLevelResourceManagers) && resourcehelper.IsPodLevelResourcesSet(pod) {
		return s.admitUsingPodResources(ctx, pod, operation)
	}
	return s.admitUsingContainerResources(ctx, pod, operation)
}

func (s *podScope) admitUsingContainerResources(ctx context.Context, pod *v1.Pod, operation lifecycle.Operation) lifecycle.PodAdmitResult {
	logger := klog.FromContext(ctx)

	var bestHint TopologyHint
	var admit bool
	if pod.Spec.NUMANode != nil {
		if operation != lifecycle.AddOperation {
			return admission.GetPodAdmitResult(numaPlacementError{fmt.Sprintf("NUMA node %d supports only new container allocation", *pod.Spec.NUMANode)})
		}
		for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
			for _, provider := range s.hintProviders {
				if checker, ok := provider.(interface {
					CheckNUMAPlacement(*v1.Pod, *v1.Container) error
				}); ok {
					if err := checker.CheckNUMAPlacement(pod, &container); err != nil {
						return admission.GetPodAdmitResult(numaPlacementError{err.Error()})
					}
				}
			}
		}
		var err error
		bestHint, err = s.requestedAffinity(logger, pod, operation)
		if err != nil {
			return admission.GetPodAdmitResult(err)
		}
		admit = true
	} else {
		bestHint, admit = s.checkAffinity(logger, pod, operation)
	}
	if !admit {
		return admission.GetPodAdmitResult(NewTopologyAffinityError())
	}

	var acquired []func() error
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		logger.Info("Topology Affinity", "bestHint", bestHint, "pod", klog.KObj(pod), "containerName", container.Name)
		s.setTopologyHints(string(pod.UID), container.Name, bestHint)

		var err error
		if pod.Spec.NUMANode != nil {
			var current []func() error
			current, err = s.allocateRequestedResources(ctx, pod, &container, operation)
			acquired = append(acquired, current...)
		} else {
			err = s.allocateAlignedResources(ctx, pod, &container, operation)
		}
		if err != nil {
			for i := len(acquired) - 1; i >= 0; i-- {
				if releaseErr := acquired[i](); releaseErr != nil {
					logger.Error(releaseErr, "Failed to release NUMA allocation after admission failure", "pod", klog.KObj(pod))
				}
			}
			metrics.TopologyManagerAdmissionErrorsTotal.Inc()
			return admission.GetPodAdmitResult(err)
		}
	}

	s.updateSuccessMetrics(logger, pod)
	return admission.GetPodAdmitResult(nil)
}

type numaPlacementError struct{ message string }

func (e numaPlacementError) Error() string { return e.message }
func (e numaPlacementError) Type() string  { return "NUMAPlacementFailed" }

type numaDeviceAffinityError struct{ message string }

func (e numaDeviceAffinityError) Error() string { return e.message }
func (e numaDeviceAffinityError) Type() string  { return ErrorTopologyAffinity }

func (s *podScope) requestedAffinity(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) (TopologyHint, error) {
	id := int(*pod.Spec.NUMANode)
	mask, err := bitmask.NewBitMask(id)
	if err != nil {
		return TopologyHint{}, numaPlacementError{fmt.Sprintf("invalid requested NUMA node %d: %v", id, err)}
	}
	providersHints := s.accumulateProvidersHints(logger, pod, operation)
	for providerIndex, resources := range providersHints {
		filteredResources := make(map[string][]TopologyHint, len(resources))
		for name, hints := range resources {
			var matching []TopologyHint
			resourceRequired := name == string(v1.ResourceCPU) || name == string(v1.ResourceMemory) || corehelper.IsHugePageResourceName(v1.ResourceName(name))
			for _, hint := range hints {
				if resourceRequired && hint.NUMANodeAffinity != nil && hint.NUMANodeAffinity.IsEqual(mask) ||
					!resourceRequired && (hint.NUMANodeAffinity == nil || hint.NUMANodeAffinity.IsSet(id)) {
					if resourceRequired {
						hint.Preferred = true
					}
					matching = append(matching, hint)
				}
			}
			if len(matching) == 0 {
				if !resourceRequired {
					return TopologyHint{}, numaDeviceAffinityError{fmt.Sprintf("NUMA node %d: %s (%s)", id, NewTopologyAffinityError(), name)}
				}
				if name == string(v1.ResourceCPU) {
					return TopologyHint{}, numaPlacementError{fmt.Sprintf("NUMA node %d has insufficient eligible CPUs or incompatible CPU topology", id)}
				}
				if corehelper.IsHugePageResourceName(v1.ResourceName(name)) {
					return TopologyHint{}, numaPlacementError{fmt.Sprintf("NUMA node %d has insufficient %s capacity or incompatible memory topology", id, name)}
				}
				return TopologyHint{}, numaPlacementError{fmt.Sprintf("NUMA node %d has insufficient ordinary memory or incompatible memory topology", id)}
			}
			filteredResources[name] = matching
		}
		providersHints[providerIndex] = filteredResources
	}
	hint, admit := s.policy.Merge(logger, providersHints)
	if !admit || hint.NUMANodeAffinity != nil && !hint.NUMANodeAffinity.IsSet(id) {
		return TopologyHint{}, numaDeviceAffinityError{fmt.Sprintf("NUMA node %d: %s", id, NewTopologyAffinityError())}
	}
	return TopologyHint{NUMANodeAffinity: mask, Preferred: true}, nil
}

func (s *podScope) allocateRequestedResources(ctx context.Context, pod *v1.Pod, container *v1.Container, operation lifecycle.Operation) ([]func() error, error) {
	logger := klog.FromContext(ctx)
	var acquired []func() error
	for _, provider := range s.hintProviders {
		snapshotter, hasSnapshot := provider.(interface {
			SnapshotNUMAPlacement(*v1.Pod, *v1.Container) func(klog.Logger) error
		})
		if hasSnapshot {
			rollback := snapshotter.SnapshotNUMAPlacement(pod, container)
			acquired = append(acquired, func() error { return rollback(logger) })
		}
		if releaser, ok := provider.(interface {
			ReleaseNUMAPlacement(klog.Logger, *v1.Pod, *v1.Container) error
		}); ok && !hasSnapshot {
			if checker, ok := provider.(interface {
				NUMAPlacementAllocated(*v1.Pod, *v1.Container) bool
			}); !ok || !checker.NUMAPlacementAllocated(pod, container) {
				acquired = append(acquired, func() error { return releaser.ReleaseNUMAPlacement(logger, pod, container) })
			}
		}
		if err := provider.Allocate(ctx, pod, container, operation); err != nil {
			return acquired, numaPlacementError{fmt.Sprintf("NUMA node %d: %v", *pod.Spec.NUMANode, err)}
		}
	}
	return acquired, nil
}

func (s *podScope) admitUsingPodResources(ctx context.Context, pod *v1.Pod, operation lifecycle.Operation) lifecycle.PodAdmitResult {
	logger := klog.FromContext(ctx)

	bestHint, admit := s.checkAffinity(logger, pod, operation)
	if !admit {
		return admission.GetPodAdmitResult(NewPodLevelTopologyAffinityError("pod with pod-level resources failed admission under pod-scope topology manager"))
	}

	// If the PodLevelResourceManagers feature is enabled, delegate the resource
	// allocation to the hint providers (CPU and Memory managers) via the AllocatePod call.
	// This is a one-time allocation for the entire pod.
	logger.V(4).Info("Calling AllocatePod on hint providers", "pod", klog.KObj(pod))

	// Store the best hint for all containers.
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		logger.Info("Topology Affinity", "bestHint", bestHint, "pod", klog.KObj(pod), "containerName", container.Name)
		s.setTopologyHints(string(pod.UID), container.Name, bestHint)
	}

	err := s.allocatePodAlignedResources(logger, pod, operation)
	if err != nil {
		logger.Error(err, "Pod-level allocation failed", "pod", klog.KObj(pod))
		metrics.TopologyManagerAdmissionErrorsTotal.Inc()
		return admission.GetPodAdmitResult(err)
	}

	s.updateSuccessMetrics(logger, pod)
	return admission.GetPodAdmitResult(nil)
}

func (s *podScope) checkAffinity(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) (TopologyHint, bool) {
	// Calculate the best NUMA affinity for the pod as a whole.
	bestHint, admit := s.calculateAffinity(logger, pod, operation)
	logger.Info("Best TopologyHint", "bestHint", bestHint, "pod", klog.KObj(pod), "operation", operation)
	if !admit {
		if IsAlignmentGuaranteed(s.policy) {
			// Increment failure metric only if alignment was guaranteed.
			metrics.ContainerAlignedComputeResourcesFailure.WithLabelValues(metrics.AlignScopePod, metrics.AlignedNUMANode).Inc()
		}
		metrics.TopologyManagerAdmissionErrorsTotal.Inc()

		return TopologyHint{}, false
	}
	return bestHint, true
}

func (s *podScope) updateSuccessMetrics(logger klog.Logger, pod *v1.Pod) {
	if IsAlignmentGuaranteed(s.policy) {
		// Increment success metric only if alignment was guaranteed.
		logger.V(4).Info("Resource alignment at pod scope guaranteed", "pod", klog.KObj(pod))
		metrics.ContainerAlignedComputeResources.WithLabelValues(metrics.AlignScopePod, metrics.AlignedNUMANode).Inc()
	}
}

func (s *podScope) accumulateProvidersHints(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) []map[string][]TopologyHint {
	var providersHints []map[string][]TopologyHint

	for _, provider := range s.hintProviders {
		// Get the TopologyHints for a Pod from a provider.
		hints := provider.GetPodTopologyHints(logger, pod, operation)
		providersHints = append(providersHints, hints)
		logger.Info("TopologyHints", "hints", hints, "pod", klog.KObj(pod), "operation", operation)
	}
	return providersHints
}

func (s *podScope) calculateAffinity(logger klog.Logger, pod *v1.Pod, operation lifecycle.Operation) (TopologyHint, bool) {
	providersHints := s.accumulateProvidersHints(logger, pod, operation)
	bestHint, admit := s.policy.Merge(logger, providersHints)
	logger.Info("PodTopologyHint", "bestHint", bestHint, "pod", klog.KObj(pod), "operation", operation)
	return bestHint, admit
}
