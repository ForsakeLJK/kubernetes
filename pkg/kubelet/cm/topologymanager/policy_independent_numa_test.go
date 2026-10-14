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
	"k8s.io/component-base/metrics/testutil"
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
