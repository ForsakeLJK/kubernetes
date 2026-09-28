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
	"strings"
	"testing"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

func TestNUMAPlacementAdmission(t *testing.T) {
	config := cm.NodeConfig{
		CPUManagerPolicy:      "static",
		MemoryManagerPolicy:   "Static",
		TopologyManagerPolicy: "single-numa-node",
		TopologyManagerScope:  "pod",
	}
	withoutPolicy := func(change func(*cm.NodeConfig)) cm.NodeConfig {
		copy := config
		change(&copy)
		return copy
	}
	info := &cadvisorapi.MachineInfo{Topology: []cadvisorapi.Node{{Id: 0}, {Id: 2}}}
	for _, tc := range []struct {
		name        string
		id          *int32
		goos        string
		config      cm.NodeConfig
		want        string
		missingInfo bool
	}{
		{name: "omitted", goos: "windows", want: ""},
		{name: "zero exists", id: numaTestID(0), goos: "linux", config: config, want: "NUMAPlacementNotImplemented"},
		{name: "noncontiguous ID exists", id: numaTestID(2), goos: "linux", config: config, want: "NUMAPlacementNotImplemented"},
		{name: "hole does not exist", id: numaTestID(1), goos: "linux", config: config, want: "NUMANodeNotFound"},
		{name: "unsupported CPU policy", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.CPUManagerPolicy = "none" }), want: "NUMAPlacementUnsupported"},
		{name: "unsupported memory policy", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.MemoryManagerPolicy = "None" }), want: "NUMAPlacementUnsupported"},
		{name: "unsupported topology policy", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.TopologyManagerPolicy = "best-effort" }), want: "NUMAPlacementUnsupported"},
		{name: "unsupported topology scope", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.TopologyManagerScope = "container" }), want: "NUMAPlacementUnsupported"},
		{name: "unsupported OS", id: numaTestID(0), goos: "windows", config: config, want: "NUMAPlacementUnsupported"},
		{name: "topology unavailable", id: numaTestID(0), goos: "linux", config: config, missingInfo: true, want: "NUMAPlacementUnsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &numaPlacementAdmitHandler{goos: tc.goos, config: tc.config, getMachineInfo: func() (*cadvisorapi.MachineInfo, error) {
				if tc.missingInfo {
					return nil, nil
				}
				return info, nil
			}}
			pod := &v1.Pod{Spec: v1.PodSpec{NUMANode: tc.id}}
			result := h.Admit(context.Background(), &lifecycle.PodAdmitAttributes{Pod: pod})
			if result.Reason != tc.want || result.Admit != (tc.want == "") {
				t.Fatalf("admission = %+v, want reason %q", result, tc.want)
			}
			if tc.id != nil && !strings.Contains(result.Message, fmt.Sprintf("NUMA node %d", *tc.id)) {
				t.Fatalf("message %q does not identify requested NUMA node", result.Message)
			}
			if tc.missingInfo && !strings.Contains(result.Message, "topology information is unavailable") {
				t.Fatalf("missing topology message is not actionable: %q", result.Message)
			}
		})
	}
}

func numaTestID(id int32) *int32 { return &id }
