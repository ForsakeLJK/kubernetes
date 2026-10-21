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
	"context"
	"runtime"
	"strings"
	"testing"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
)

func TestIndependentNUMAManagerConfiguration(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	for _, scope := range []string{PodTopologyScope, ContainerTopologyScope} {
		t.Run(scope, func(t *testing.T) {
			manager, err := NewManager(logger, []cadvisorapi.Node{{Id: 0}, {Id: 2}}, "independent-numa", scope, nil)
			if runtime.GOOS != "linux" {
				if err == nil || !strings.Contains(err.Error(), "requires Linux") {
					t.Fatalf("manager error = %v, want Linux requirement", err)
				}
				return
			}
			if scope == ContainerTopologyScope {
				if err == nil || !strings.Contains(err.Error(), "requires pod scope") {
					t.Fatalf("manager error = %v, want pod scope requirement", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if manager.GetPolicy().Name() != "independent-numa" || manager.Name() != PodTopologyScope {
				t.Fatalf("manager = %s/%s, want independent-numa/pod", manager.GetPolicy().Name(), manager.Name())
			}
		})
	}
}

func TestIndependentNUMAHintsDoNotEstablishCommonAlignment(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	policy := NewIndependentNUMAPolicy(&NUMAInfo{Nodes: []int{0, 2}}, PolicyOptions{})
	if IsAlignmentGuaranteed(policy) {
		t.Fatal("independent-numa must not globally promise common CPU/memory alignment")
	}
	metrics.Register()
	for _, legacy := range []bool{false, true} {
		pod := &v1.Pod{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "app"}}}}
		if legacy {
			node := int32(2)
			pod.Spec.NUMANode = &node
		}
		scope := NewPodScope(policy)
		scope.AddHintProvider(logger, &mockHintProvider{th: map[string][]TopologyHint{
			"cpu":    {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}},
			"memory": {{NUMANodeAffinity: NewTestBitMask(2), Preferred: true}},
		}})
		counter := metrics.ContainerAlignedComputeResources.WithLabelValues(metrics.AlignScopePod, metrics.AlignedNUMANode)
		before, err := testutil.GetCounterMetricValue(counter)
		if err != nil {
			t.Fatal(err)
		}
		result := scope.Admit(context.Background(), pod, lifecycle.AddOperation)
		if !result.Admit {
			t.Fatalf("common placement refused: %+v", result)
		}
		after, err := testutil.GetCounterMetricValue(counter)
		if err != nil {
			t.Fatal(err)
		}
		if after-before != 0 {
			t.Fatalf("legacy=%v: hints without concrete placement incremented alignment by %v", legacy, after-before)
		}
	}
}

func TestIndependentNUMACommonMerge(t *testing.T) {
	info := commonNUMAInfoFourNodes()
	single := &singleNumaNodePolicy{numaInfo: info}
	cases := append(commonPolicyMergeTestCases(info.Nodes), single.mergeTestCases(info.Nodes)...)
	for _, closest := range []bool{false, true} {
		policy := NewIndependentNUMAPolicy(info, PolicyOptions{PreferClosestNUMA: closest})
		testPolicyMerge(policy, cases, t)
	}
}

func TestIndependentNUMAManagerOptions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("independent-numa manager requires Linux")
	}
	logger, _ := ktesting.NewTestContext(t)
	topology := make([]cadvisorapi.Node, 9)
	for i := range topology {
		topology[i].Id = i
		topology[i].Distances = make([]uint64, 9)
	}
	for _, tc := range []struct {
		name    string
		options map[string]string
		want    string
	}{
		{name: "default node limit", want: "more than 8 NUMA Nodes"},
		{name: "raised node limit", options: map[string]string{MaxAllowableNUMANodes: "9"}},
		{name: "closest option remains supported", options: map[string]string{MaxAllowableNUMANodes: "9", PreferClosestNUMANodes: "true"}},
		{name: "unknown option", options: map[string]string{"unknown": "true"}, want: "unknown Topology Manager Policy option"},
		{name: "invalid closest option", options: map[string]string{PreferClosestNUMANodes: "invalid"}, want: "bad value for option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewManager(logger, topology, PolicyIndependentNUMA, PodTopologyScope, tc.options)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("manager error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestIndependentNUMADistanceRanking(t *testing.T) {
	// Columns follow ascending node IDs [0 2 3]; node 3 is CPU-less.
	for _, tc := range []struct {
		name      string
		distances NUMADistances
		want      NUMAPlacement
		wantErr   string
	}{
		{name: "relevant columns rank", distances: NUMADistances{0: {10, 20, 40}, 2: {20, 10, 30}}, want: NUMAPlacement{CPU: 2, Memory: 3}},
		{name: "unrelated malformed column ignored", distances: NUMADistances{0: {10, 0, 40}, 2: {255, 10, 30}}, want: NUMAPlacement{CPU: 2, Memory: 3}},
		{name: "relevant malformed column refused", distances: NUMADistances{0: {10, 20, 0}, 2: {20, 10, 30}}, wantErr: "from node 0 to node 3"},
		{name: "invalid local anchor refused", distances: NUMADistances{0: {20, 20, 40}, 2: {20, 10, 30}}, wantErr: "from node 0 to node 0"},
		{name: "truncated row refused", distances: NUMADistances{0: {10, 20}, 2: {20, 10, 30}}, wantErr: "distance columns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &NUMAInfo{Nodes: []int{3, 0, 2}, NUMADistances: tc.distances}
			got, err := info.closestPlacement([]NUMAPlacement{{CPU: 0, Memory: 3}, {CPU: 2, Memory: 3}})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ranking error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.CPU != tc.want.CPU || got.Memory != tc.want.Memory {
				t.Fatalf("ranking = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestIndependentNUMAInfoClosestOption(t *testing.T) {
	topology := []cadvisorapi.Node{{Id: 0}, {Id: 2}}
	if _, err := newNUMAInfo(topology, PolicyOptions{}, true); err != nil {
		t.Fatalf("independent-numa without the closest option must load missing distances: %v", err)
	}
	if _, err := newNUMAInfo(topology, PolicyOptions{PreferClosestNUMA: true}, true); err == nil {
		t.Fatal("prefer-closest-numa-nodes must still refuse startup without distances under independent-numa")
	}
}

type numaLocalityProvider struct {
	mockHintProvider
	cpu map[string]sets.Set[int]
}

func (p *numaLocalityProvider) GetCPUNUMANodes(_, container string) sets.Set[int] {
	return p.cpu[container]
}

func (p *numaLocalityProvider) GetMemoryNUMANodes(klog.Logger, *v1.Pod, *v1.Container) sets.Set[int] {
	return sets.New(2)
}

func TestIndependentNUMACommonPlacementSkipsCompletedInit(t *testing.T) {
	logger, _ := ktesting.NewTestContext(t)
	node := int32(2)
	pod := &v1.Pod{
		Spec: v1.PodSpec{CPUNUMA: &node, InitContainers: []v1.Container{{Name: "init"}}, Containers: []v1.Container{{Name: "app"}}},
		Status: v1.PodStatus{InitContainerStatuses: []v1.ContainerStatus{
			{Name: "init", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}},
		}},
	}
	// A released init assignment reports the shared pool across both nodes.
	provider := &numaLocalityProvider{cpu: map[string]sets.Set[int]{"init": sets.New(0, 2), "app": sets.New(2)}}
	scope := NewPodScope(NewIndependentNUMAPolicy(&NUMAInfo{Nodes: []int{0, 2}}, PolicyOptions{})).(*podScope)
	scope.AddHintProvider(logger, provider)
	if !scope.hasCommonNUMAPlacement(logger, pod) {
		t.Fatal("completed init container prevented common-node alignment from being counted")
	}
	pod.Status.InitContainerStatuses = nil
	if scope.hasCommonNUMAPlacement(logger, pod) {
		t.Fatal("running init container on the shared pool was counted as common-node alignment")
	}
}

func TestNUMAPlacementDescriptions(t *testing.T) {
	two := int32(2)
	for _, tc := range []struct {
		name                   string
		spec                   v1.PodSpec
		placement, cpu, memory string
	}{
		{name: "legacy", spec: v1.PodSpec{NUMANode: &two}, placement: "NUMA node 2", cpu: "requested NUMA node 2", memory: "requested NUMA node 2"},
		{name: "memory only", spec: v1.PodSpec{MemNUMA: &two}, placement: "NUMA placement cpuNUMA=automatic, memNUMA=2", cpu: "automatic CPU NUMA placement", memory: "requested NUMA node 2"},
		{name: "CPU only", spec: v1.PodSpec{CPUNUMA: &two}, placement: "NUMA placement cpuNUMA=2, memNUMA=automatic", cpu: "requested NUMA node 2", memory: "automatic memory NUMA placement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			placement := ResolveNUMAPlacement(&v1.Pod{Spec: tc.spec})
			if placement.String() != tc.placement || placement.CPUTarget() != tc.cpu || placement.MemoryTarget() != tc.memory {
				t.Fatalf("descriptions = %q / %q / %q, want %q / %q / %q", placement, placement.CPUTarget(), placement.MemoryTarget(), tc.placement, tc.cpu, tc.memory)
			}
		})
	}
}

func TestIndependentNUMAResizeRefusalNamesFields(t *testing.T) {
	node := int32(3)
	resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")}
	pod := &v1.Pod{Spec: v1.PodSpec{MemNUMA: &node, Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: resources, Limits: resources}}}}}
	scope := NewPodScope(NewIndependentNUMAPolicy(&NUMAInfo{Nodes: []int{0, 3}}, PolicyOptions{}))
	result := scope.Admit(context.Background(), pod, lifecycle.ResizeOperation)
	if result.Admit || strings.Contains(result.Message, "-1") || !strings.Contains(result.Message, "cpuNUMA=automatic, memNUMA=3") {
		t.Fatalf("resize refusal = %+v, want both targets named without node -1", result)
	}
}
