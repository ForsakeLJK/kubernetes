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
	"errors"
	"reflect"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/test/utils/ktesting"
)

type numaPlacementTestProvider struct {
	mockHintProvider
	allocateError error
	failContainer string
	allocated     bool
	released      bool
	assignments   map[string]bool
}

func (p *numaPlacementTestProvider) Allocate(_ context.Context, _ *v1.Pod, container *v1.Container, _ lifecycle.Operation) error {
	if p.allocateError != nil && (p.failContainer == "" || p.failContainer == container.Name) {
		return p.allocateError
	}
	p.allocated = true
	if p.assignments != nil {
		p.assignments[container.Name] = true
	}
	return nil
}

func (p *numaPlacementTestProvider) ReleaseNUMAPlacement(_ klog.Logger, _ *v1.Pod, container *v1.Container) error {
	p.released = true
	p.allocated = false
	delete(p.assignments, container.Name)
	return nil
}

func (p *numaPlacementTestProvider) NUMAPlacementAllocated(_ *v1.Pod, container *v1.Container) bool {
	if p.assignments != nil {
		return p.assignments[container.Name]
	}
	return p.allocated
}

func TestRequestedNUMAPlacementRollsBackWholePod(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	info := &NUMAInfo{Nodes: []int{0, 2}, NUMADistances: NUMADistances{0: {10, 20}, 2: {20, 10}}}
	node := int32(2)
	pod := &v1.Pod{Spec: v1.PodSpec{NUMANode: &node, InitContainers: []v1.Container{{Name: "init"}}, Containers: []v1.Container{{Name: "app-a"}, {Name: "app-b"}}}}
	hints := map[string][]TopologyHint{"cpu": {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}}
	for _, fail := range []string{"init", "app-a", "app-b"} {
		t.Run(fail, func(t *testing.T) {
			first := &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: hints}, assignments: map[string]bool{"init": true}}
			second := &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: hints}, assignments: map[string]bool{}, failContainer: fail, allocateError: errors.New("injected allocation failure")}
			s := NewPodScope(NewSingleNumaNodePolicy(info, PolicyOptions{})).(*podScope)
			s.AddHintProvider(logger, first)
			s.AddHintProvider(logger, second)
			result := s.Admit(ctx, pod, lifecycle.AddOperation)
			if result.Admit || !strings.Contains(result.Message, "injected allocation failure") {
				t.Fatalf("admission = %+v, want injected failure", result)
			}
			if !reflect.DeepEqual(first.assignments, map[string]bool{"init": true}) || len(second.assignments) != 0 {
				t.Fatalf("partial allocation retained: first=%v second=%v", first.assignments, second.assignments)
			}
		})
	}
}

func TestRequestedNUMAPlacement(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	info := &NUMAInfo{Nodes: []int{0, 2}, NUMADistances: NUMADistances{0: {10, 20}, 2: {20, 10}}}
	node := int32(2)
	newPod := func() *v1.Pod {
		return &v1.Pod{Spec: v1.PodSpec{NUMANode: &node, Containers: []v1.Container{{Name: "app"}}}}
	}
	for _, tc := range []struct {
		name   string
		cpu    []TopologyHint
		memory []TopologyHint
		device []TopologyHint
		fail   string
	}{
		{name: "requested node beats other preferred node", cpu: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}, {NUMANodeAffinity: NewTestBitMask(2), Preferred: false}}, memory: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}, {NUMANodeAffinity: NewTestBitMask(2), Preferred: false}}},
		{name: "CPU capacity elsewhere", cpu: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}}, memory: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}, fail: "cpu"},
		{name: "memory capacity elsewhere", cpu: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}, memory: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}}, fail: "memory"},
		{name: "device conflict", cpu: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}, memory: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}, device: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}}, fail: "device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewPodScope(NewSingleNumaNodePolicy(info, PolicyOptions{})).(*podScope)
			s.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"cpu": tc.cpu}}})
			s.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"memory": tc.memory}}})
			if tc.device != nil {
				s.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"device": tc.device}}})
			}
			pod := newPod()
			result := s.Admit(ctx, pod, lifecycle.AddOperation)
			if tc.fail != "" {
				wantReason := "NUMAPlacementFailed"
				if tc.fail == "device" {
					wantReason = ErrorTopologyAffinity
				}
				if result.Admit || result.Reason != wantReason || !strings.Contains(strings.ToLower(result.Message), tc.fail) || !strings.Contains(result.Message, "NUMA node 2") {
					t.Fatalf("admission = %+v, want %s failure", result, tc.fail)
				}
				return
			}
			if !result.Admit || !s.GetAffinity(logger, string(pod.UID), "app").NUMANodeAffinity.IsEqual(NewTestBitMask(2)) {
				t.Fatalf("requested node was not selected: %+v", result)
			}
		})
	}
	omitted := newPod()
	omitted.Spec.NUMANode = nil
	omittedScope := NewPodScope(NewSingleNumaNodePolicy(info, PolicyOptions{})).(*podScope)
	omittedHints := []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}, {NUMANodeAffinity: NewTestBitMask(2), Preferred: false}}
	omittedScope.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"cpu": omittedHints}}})
	omittedScope.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"memory": omittedHints}}})
	if result := omittedScope.Admit(ctx, omitted, lifecycle.AddOperation); !result.Admit || !omittedScope.GetAffinity(logger, string(omitted.UID), "app").NUMANodeAffinity.IsEqual(NewTestBitMask(0)) {
		t.Fatalf("omitted-field topology selection changed: %+v", result)
	}
	first := &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"cpu": {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}}}}
	second := &numaPlacementTestProvider{
		mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"memory": {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}}},
		allocateError:    errors.New("injected memory failure"),
	}
	s := NewPodScope(NewSingleNumaNodePolicy(info, PolicyOptions{})).(*podScope)
	s.AddHintProvider(logger, first)
	s.AddHintProvider(logger, second)
	result := s.Admit(ctx, newPod(), lifecycle.AddOperation)
	if result.Admit || !first.released || first.allocated || !strings.Contains(result.Message, "injected memory failure") {
		t.Fatalf("partial allocation was not released: %+v, first=%+v", result, first)
	}
	first.allocated, first.released = true, false
	result = s.Admit(ctx, newPod(), lifecycle.AddOperation)
	if result.Admit || first.released || !first.allocated {
		t.Fatalf("retry removed an existing assignment: %+v, first=%+v", result, first)
	}
}

func TestRequestedNUMAHugepageHints(t *testing.T) {
	logger, ctx := ktesting.NewTestContext(t)
	info := &NUMAInfo{Nodes: []int{0, 2}, NUMADistances: NUMADistances{0: {10, 20}, 2: {20, 10}}}
	node := int32(2)
	pageName := "hugepages-2Mi"
	for _, tc := range []struct {
		name  string
		hints []TopologyHint
		fail  bool
	}{
		{name: "local hugepages available", hints: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}, {NUMANodeAffinity: NewTestBitMask(2), Preferred: false}}},
		{name: "hugepages only elsewhere", hints: []TopologyHint{{NUMANodeAffinity: NewTestBitMask(0), Preferred: true}}, fail: true},
		{name: "hugepages exhausted", hints: []TopologyHint{}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := &v1.Pod{Spec: v1.PodSpec{NUMANode: &node, Containers: []v1.Container{{Name: "app"}}}}
			s := NewPodScope(NewSingleNumaNodePolicy(info, PolicyOptions{})).(*podScope)
			s.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"cpu": {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}}}})
			s.AddHintProvider(logger, &numaPlacementTestProvider{mockHintProvider: mockHintProvider{th: map[string][]TopologyHint{"memory": {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}}, pageName: tc.hints}}})
			result := s.Admit(ctx, pod, lifecycle.AddOperation)
			if tc.fail {
				if result.Admit || result.Reason != "NUMAPlacementFailed" || !strings.Contains(result.Message, pageName) || !strings.Contains(result.Message, "NUMA node 2") {
					t.Fatalf("admission = %+v, want local %s shortage", result, pageName)
				}
				return
			}
			if !result.Admit || !s.GetAffinity(logger, string(pod.UID), "app").NUMANodeAffinity.IsEqual(NewTestBitMask(2)) {
				t.Fatalf("hugepage hint changed requested affinity: %+v", result)
			}
		})
	}
}

func TestPodCalculateAffinity(t *testing.T) {
	tcases := []struct {
		name     string
		hp       []HintProvider
		expected []map[string][]TopologyHint
	}{
		{
			name:     "No hint providers",
			hp:       []HintProvider{},
			expected: ([]map[string][]TopologyHint)(nil),
		},
		{
			name: "HintProvider returns empty non-nil map[string][]TopologyHint",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{},
				},
			},
			expected: []map[string][]TopologyHint{
				{},
			},
		},
		{
			name: "HintProvider returns -nil map[string][]TopologyHint from provider",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource": nil,
					},
				},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource": nil,
				},
			},
		},
		{
			name: "Assorted HintProviders",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource-1/A": {
							{NUMANodeAffinity: NewTestBitMask(0), Preferred: true},
							{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: false},
						},
						"resource-1/B": {
							{NUMANodeAffinity: NewTestBitMask(1), Preferred: true},
							{NUMANodeAffinity: NewTestBitMask(1, 2), Preferred: false},
						},
					},
				},
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource-2/A": {
							{NUMANodeAffinity: NewTestBitMask(2), Preferred: true},
							{NUMANodeAffinity: NewTestBitMask(3, 4), Preferred: false},
						},
						"resource-2/B": {
							{NUMANodeAffinity: NewTestBitMask(2), Preferred: true},
							{NUMANodeAffinity: NewTestBitMask(3, 4), Preferred: false},
						},
					},
				},
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource-3": nil,
					},
				},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource-1/A": {
						{NUMANodeAffinity: NewTestBitMask(0), Preferred: true},
						{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: false},
					},
					"resource-1/B": {
						{NUMANodeAffinity: NewTestBitMask(1), Preferred: true},
						{NUMANodeAffinity: NewTestBitMask(1, 2), Preferred: false},
					},
				},
				{
					"resource-2/A": {
						{NUMANodeAffinity: NewTestBitMask(2), Preferred: true},
						{NUMANodeAffinity: NewTestBitMask(3, 4), Preferred: false},
					},
					"resource-2/B": {
						{NUMANodeAffinity: NewTestBitMask(2), Preferred: true},
						{NUMANodeAffinity: NewTestBitMask(3, 4), Preferred: false},
					},
				},
				{
					"resource-3": nil,
				},
			},
		},
	}

	logger, _ := ktesting.NewTestContext(t)

	for _, tc := range tcases {
		podScope := &podScope{
			scope{
				hintProviders: tc.hp,
				policy:        &mockPolicy{},
				name:          PodTopologyScope,
			},
		}

		podScope.calculateAffinity(logger, &v1.Pod{}, lifecycle.AddOperation)
		actual := podScope.policy.(*mockPolicy).ph
		if !reflect.DeepEqual(tc.expected, actual) {
			t.Errorf("Test Case: %s", tc.name)
			t.Errorf("Expected result to be %v, got %v", tc.expected, actual)
		}
	}
}

func TestPodAccumulateProvidersHints(t *testing.T) {
	tcases := []struct {
		name     string
		hp       []HintProvider
		expected []map[string][]TopologyHint
	}{
		{
			name:     "TopologyHint not set",
			hp:       []HintProvider{},
			expected: nil,
		},
		{
			name: "HintProvider returns empty non-nil map[string][]TopologyHint",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{},
				},
			},
			expected: []map[string][]TopologyHint{
				{},
			},
		},
		{
			name: "HintProvider returns - nil map[string][]TopologyHint from provider",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource": nil,
					},
				},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource": nil,
				},
			},
		},
		{
			name: "2 HintProviders with 1 resource returns hints",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource1": {TopologyHint{}},
					},
				},
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource2": {TopologyHint{}},
					},
				},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource1": {TopologyHint{}},
				},
				{
					"resource2": {TopologyHint{}},
				},
			},
		},
		{
			name: "2 HintProviders 1 with 1 resource 1 with nil hints",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource1": {TopologyHint{}},
					},
				},
				&mockHintProvider{nil},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource1": {TopologyHint{}},
				},
				nil,
			},
		},
		{
			name: "2 HintProviders 1 with 1 resource 1 empty hints",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource1": {TopologyHint{}},
					},
				},
				&mockHintProvider{
					map[string][]TopologyHint{},
				},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource1": {TopologyHint{}},
				},
				{},
			},
		},
		{
			name: "HintProvider with 2 resources returns hints",
			hp: []HintProvider{
				&mockHintProvider{
					map[string][]TopologyHint{
						"resource1": {TopologyHint{}},
						"resource2": {TopologyHint{}},
					},
				},
			},
			expected: []map[string][]TopologyHint{
				{
					"resource1": {TopologyHint{}},
					"resource2": {TopologyHint{}},
				},
			},
		},
	}

	logger, _ := ktesting.NewTestContext(t)

	for _, tc := range tcases {
		pScope := podScope{
			scope{
				hintProviders: tc.hp,
			},
		}
		actual := pScope.accumulateProvidersHints(logger, &v1.Pod{}, lifecycle.AddOperation)
		if !reflect.DeepEqual(actual, tc.expected) {
			t.Errorf("Test Case %s: Expected NUMANodeAffinity in result to be %v, got %v", tc.name, tc.expected, actual)
		}
	}
}
