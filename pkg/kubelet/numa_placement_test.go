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
	"strings"
	"testing"

	cadvisorapi "github.com/google/cadvisor/lib/model"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

func TestNUMAPlacementAdmission(t *testing.T) {
	for _, policy := range []string{"single-numa-node", "independent-numa"} {
		t.Run(policy, func(t *testing.T) {
			config := cm.NodeConfig{
				CPUManagerPolicy:      "static",
				MemoryManagerPolicy:   "Static",
				TopologyManagerPolicy: policy,
				TopologyManagerScope:  "pod",
			}
			withoutPolicy := func(change func(*cm.NodeConfig)) cm.NodeConfig {
				copy := config
				change(&copy)
				return copy
			}
			info := &cadvisorapi.MachineInfo{Topology: []cadvisorapi.Node{{Id: 0}, {Id: 2}}}
			for _, tc := range []struct {
				name            string
				id              *int32
				goos            string
				config          cm.NodeConfig
				want            string
				missingInfo     bool
				singleContainer bool
			}{
				{name: "omitted", goos: "windows", want: ""},
				{name: "zero exists", id: numaTestID(0), goos: "linux", config: config},
				{name: "noncontiguous ID exists", id: numaTestID(2), goos: "linux", config: config},
				{name: "hole does not exist", id: numaTestID(1), goos: "linux", config: config, want: "NUMANodeNotFound"},
				{name: "unsupported CPU policy", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.CPUManagerPolicy = "none" }), want: "NUMAPlacementUnsupported"},
				{name: "unsupported memory policy", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.MemoryManagerPolicy = "None" }), want: "NUMAPlacementUnsupported"},
				{name: "unsupported topology policy", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.TopologyManagerPolicy = "best-effort" }), want: "NUMAPlacementUnsupported"},
				{name: "unsupported topology scope", id: numaTestID(0), goos: "linux", config: withoutPolicy(func(c *cm.NodeConfig) { c.TopologyManagerScope = "container" }), want: "NUMAPlacementUnsupported"},
				{name: "unsupported OS", id: numaTestID(0), goos: "windows", config: config, want: "NUMAPlacementUnsupported"},
				{name: "topology unavailable", id: numaTestID(0), goos: "linux", config: config, missingInfo: true, want: "NUMAPlacementUnsupported"},
				{name: "single container is supported", id: numaTestID(2), goos: "linux", config: config, want: "", singleContainer: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					h := &numaPlacementAdmitHandler{goos: tc.goos, config: tc.config, getMachineInfo: func() (*cadvisorapi.MachineInfo, error) {
						if tc.missingInfo {
							return nil, nil
						}
						return info, nil
					}}
					pod := &v1.Pod{Spec: v1.PodSpec{NUMANode: tc.id}}
					if tc.singleContainer {
						pod.Spec.Containers = []v1.Container{{Name: "workload"}}
					}
					result := h.Admit(context.Background(), &lifecycle.PodAdmitAttributes{Pod: pod})
					if result.Reason != tc.want || result.Admit != (tc.want == "") {
						t.Fatalf("admission = %+v, want reason %q", result, tc.want)
					}
					if tc.id != nil && tc.want != "" && !strings.Contains(result.Message, fmt.Sprintf("NUMA node %d", *tc.id)) {
						t.Fatalf("message %q does not identify requested NUMA node", result.Message)
					}
					if tc.missingInfo && !strings.Contains(result.Message, "topology information is unavailable") {
						t.Fatalf("missing topology message is not actionable: %q", result.Message)
					}
				})
			}
		})
	}
}

func numaTestID(id int32) *int32 { return &id }

func TestNUMAPlacementAdmitsHugepageWorkloads(t *testing.T) {
	config := cm.NodeConfig{CPUManagerPolicy: "static", MemoryManagerPolicy: "Static", TopologyManagerPolicy: "single-numa-node", TopologyManagerScope: "pod"}
	h := &numaPlacementAdmitHandler{goos: "linux", config: config, getMachineInfo: func() (*cadvisorapi.MachineInfo, error) {
		return &cadvisorapi.MachineInfo{Topology: []cadvisorapi.Node{{Id: 0}}}, nil
	}}
	hugepages := v1.ResourceName(v1.ResourceHugePagesPrefix + "2Mi")
	hugepageResources := v1.ResourceList{
		v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi"), hugepages: resource.MustParse("2Mi"),
	}
	for _, tc := range []struct {
		name string
		spec v1.PodSpec
	}{
		{name: "multiple applications", spec: v1.PodSpec{Containers: []v1.Container{{Name: "one"}, {Name: "two"}}}},
		{name: "init container", spec: v1.PodSpec{Containers: []v1.Container{{Name: "app"}}, InitContainers: []v1.Container{{Name: "init"}}}},
		{name: "hugepages", spec: v1.PodSpec{Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: hugepageResources, Limits: hugepageResources}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := &v1.Pod{Spec: tc.spec}
			pod.Spec.NUMANode = numaTestID(0)
			result := h.Admit(context.Background(), &lifecycle.PodAdmitAttributes{Pod: pod})
			if !result.Admit {
				t.Fatalf("workload admission = %+v, want admission", result)
			}
		})
	}
}

func TestIndependentNUMAPlacementAdmission(t *testing.T) {
	config := cm.NodeConfig{CPUManagerPolicy: "static", MemoryManagerPolicy: "Static", TopologyManagerPolicy: "independent-numa", TopologyManagerScope: "pod"}
	resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")}
	for _, tc := range []struct {
		name string
		edit func(*v1.Pod, *cm.NodeConfig)
		want string
	}{
		{name: "CPU-less memory target"},
		{name: "equal targets", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = numaTestID(0) }},
		{name: "whole CPU in millicores", edit: func(p *v1.Pod, _ *cm.NodeConfig) {
			p.Spec.Containers[0].Resources.Requests[v1.ResourceCPU] = resource.MustParse("1000m")
			p.Spec.Containers[0].Resources.Limits[v1.ResourceCPU] = resource.MustParse("1.0")
		}},
		{name: "CPU just below a whole number", edit: func(p *v1.Pod, _ *cm.NodeConfig) {
			p.Spec.Containers[0].Resources.Requests[v1.ResourceCPU] = resource.MustParse("0.9999")
			p.Spec.Containers[0].Resources.Limits[v1.ResourceCPU] = resource.MustParse("0.9999")
		}, want: "whole number"},
		{name: "sparse memory hole", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = numaTestID(1) }, want: "NUMANodeNotFound"},
		{name: "CPU-less CPU target", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.CPUNUMA = numaTestID(3) }, want: "eligible CPUs"},
		{name: "legacy policy", edit: func(_ *v1.Pod, c *cm.NodeConfig) { c.TopologyManagerPolicy = "single-numa-node" }, want: "independent-numa"},
		{name: "CPU partial", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = nil }},
		{name: "CPU partial missing CPU node", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = nil; p.Spec.CPUNUMA = numaTestID(1) }, want: "NUMANodeNotFound"},
		{name: "CPU partial CPU-less CPU node", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = nil; p.Spec.CPUNUMA = numaTestID(3) }, want: "eligible CPUs"},
		{name: "CPU partial negative CPU", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = nil; p.Spec.CPUNUMA = numaTestID(-1) }, want: "between 0 and 63"},
		{name: "CPU partial out-of-range CPU", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.MemNUMA = nil; p.Spec.CPUNUMA = numaTestID(64) }, want: "between 0 and 63"},
		{name: "memory partial", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.CPUNUMA = nil }},
		{name: "mixed fields", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.NUMANode = numaTestID(0) }, want: "cannot be combined"},
		{name: "multiple applications", edit: func(p *v1.Pod, _ *cm.NodeConfig) {
			p.Spec.Containers = append(p.Spec.Containers, *p.Spec.Containers[0].DeepCopy())
			p.Spec.Containers[1].Name = "second"
		}},
		{name: "init", edit: func(p *v1.Pod, _ *cm.NodeConfig) {
			p.Spec.InitContainers = []v1.Container{*p.Spec.Containers[0].DeepCopy()}
			p.Spec.InitContainers[0].Name = "init"
		}},
		{name: "hugepages", edit: func(p *v1.Pod, _ *cm.NodeConfig) {
			p.Spec.Containers[0].Resources.Requests[v1.ResourceName("hugepages-2Mi")] = resource.MustParse("2Mi")
			p.Spec.Containers[0].Resources.Limits[v1.ResourceName("hugepages-2Mi")] = resource.MustParse("2Mi")
		}},
		{name: "fractional CPUs", edit: func(p *v1.Pod, _ *cm.NodeConfig) {
			p.Spec.Containers[0].Resources.Requests[v1.ResourceCPU] = resource.MustParse("500m")
			p.Spec.Containers[0].Resources.Limits[v1.ResourceCPU] = resource.MustParse("500m")
		}, want: "whole number"},
		{name: "pod budget", edit: func(p *v1.Pod, _ *cm.NodeConfig) { p.Spec.Resources = &v1.ResourceRequirements{} }, want: "pod-level resources"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := &v1.Pod{Spec: v1.PodSpec{CPUNUMA: numaTestID(0), MemNUMA: numaTestID(3), Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: resources.DeepCopy(), Limits: resources.DeepCopy()}}}}}
			configuration := config
			if tc.edit != nil {
				tc.edit(pod, &configuration)
			}
			h := &numaPlacementAdmitHandler{goos: "linux", config: configuration, getMachineInfo: func() (*cadvisorapi.MachineInfo, error) {
				return &cadvisorapi.MachineInfo{Topology: []cadvisorapi.Node{{Id: 0, Cores: []cadvisorapi.Core{{Threads: []int{0, 1}}}}, {Id: 3}}}, nil
			}}
			result := h.Admit(context.Background(), &lifecycle.PodAdmitAttributes{Pod: pod})
			if tc.want == "" && !result.Admit || tc.want != "" && (result.Admit || !strings.Contains(result.Reason+" "+result.Message, tc.want)) {
				t.Fatalf("admission = %+v, want %q", result, tc.want)
			}
		})
	}
}

func TestIndependentNUMAPodWideDeclarations(t *testing.T) {
	config := cm.NodeConfig{CPUManagerPolicy: "static", MemoryManagerPolicy: "Static", TopologyManagerPolicy: "independent-numa", TopologyManagerScope: "pod"}
	h := &numaPlacementAdmitHandler{goos: "linux", config: config, getMachineInfo: func() (*cadvisorapi.MachineInfo, error) {
		return &cadvisorapi.MachineInfo{Topology: []cadvisorapi.Node{{Id: 0, Cores: []cadvisorapi.Core{{Threads: []int{0, 1}}}}, {Id: 3}}}, nil
	}}
	for _, form := range []string{"explicit", "CPU-only", "memory-only"} {
		for _, kind := range []string{"application", "init", "sidecar"} {
			for _, invalid := range []string{"valid", "fractional CPU", "unequal memory", "hugepages", "valid hugepages"} {
				t.Run(form+"/"+kind+"/"+invalid, func(t *testing.T) {
					resources := v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("256Mi")}
					app := v1.Container{Name: "app", Resources: v1.ResourceRequirements{Requests: resources.DeepCopy(), Limits: resources.DeepCopy()}}
					other := app.DeepCopy()
					other.Name = "other"
					switch invalid {
					case "fractional CPU":
						other.Resources.Requests[v1.ResourceCPU] = resource.MustParse("0.9999")
						other.Resources.Limits[v1.ResourceCPU] = resource.MustParse("0.9999")
					case "unequal memory":
						other.Resources.Limits[v1.ResourceMemory] = resource.MustParse("512Mi")
					case "valid hugepages":
						other.Resources.Requests["hugepages-2Mi"] = resource.MustParse("2Mi")
						other.Resources.Limits["hugepages-2Mi"] = resource.MustParse("2Mi")
						other.Resources.Requests["hugepages-1Gi"] = resource.MustParse("1Gi")
						other.Resources.Limits["hugepages-1Gi"] = resource.MustParse("1Gi")
					case "hugepages":
						other.Resources.Limits["hugepages-2Mi"] = resource.MustParse("2Mi")
					}
					pod := &v1.Pod{Spec: v1.PodSpec{CPUNUMA: numaTestID(0), MemNUMA: numaTestID(3), Containers: []v1.Container{app}}}
					if form == "CPU-only" {
						pod.Spec.MemNUMA = nil
					}
					if form == "memory-only" {
						pod.Spec.CPUNUMA = nil
					}
					if kind == "application" {
						pod.Spec.Containers = append(pod.Spec.Containers, *other)
					} else {
						if kind == "sidecar" {
							restart := v1.ContainerRestartPolicyAlways
							other.RestartPolicy = &restart
						}
						pod.Spec.InitContainers = []v1.Container{*other}
					}
					pod.Status.QOSClass = v1.PodQOSGuaranteed
					result := h.Admit(context.Background(), &lifecycle.PodAdmitAttributes{Pod: pod})
					if result.Admit != (invalid == "valid" || invalid == "valid hugepages") {
						t.Fatalf("admission = %+v", result)
					}
				})
			}
		}
	}
}
