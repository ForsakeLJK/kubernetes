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

package validation

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	podtest "k8s.io/kubernetes/pkg/api/pod/testing"
	"k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/utils/ptr"
)

func numaValidationPod(id *int32) *core.Pod {
	resources := core.ResourceRequirements{
		Requests: core.ResourceList{core.ResourceCPU: resource.MustParse("2"), core.ResourceMemory: resource.MustParse("256Mi")},
		Limits:   core.ResourceList{core.ResourceCPU: resource.MustParse("2"), core.ResourceMemory: resource.MustParse("256Mi")},
	}
	pod := podtest.MakePod("numa", podtest.SetContainers(podtest.MakeContainer("app", podtest.SetContainerResources(resources))))
	pod.Spec.NUMANode = id
	return pod
}

func TestPodNUMAPlacementValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*core.Pod)
		want string
	}{
		{name: "omitted", edit: func(p *core.Pod) { p.Spec.NUMANode = nil }},
		{name: "zero"},
		{name: "maximum", edit: func(p *core.Pod) { p.Spec.NUMANode = ptr.To[int32](63) }},
		{name: "negative", edit: func(p *core.Pod) { p.Spec.NUMANode = ptr.To[int32](-1) }, want: "spec.numaNode"},
		{name: "too large", edit: func(p *core.Pod) { p.Spec.NUMANode = ptr.To[int32](64) }, want: "spec.numaNode"},
		{name: "fractional CPU", edit: func(p *core.Pod) {
			p.Spec.Containers[0].Resources.Requests[core.ResourceCPU] = resource.MustParse("1500m")
		}, want: "positive whole number"},
		{name: "missing memory limit", edit: func(p *core.Pod) { delete(p.Spec.Containers[0].Resources.Limits, core.ResourceMemory) }, want: "memory request and limit"},
		{name: "pod budget", edit: func(p *core.Pod) { p.Spec.Resources = &core.ResourceRequirements{} }, want: "pod-level resources"},
		{name: "Windows Pod", edit: func(p *core.Pod) { p.Spec.OS = &core.PodOS{Name: core.Windows} }, want: "numaNode requires a Linux Pod"},
		{name: "init container", edit: func(p *core.Pod) { p.Spec.InitContainers = []core.Container{{Name: "init", Image: "busybox"}} }, want: "spec.initContainers[0].resources"},
		{name: "sidecar", edit: func(p *core.Pod) {
			p.Spec.InitContainers = []core.Container{{Name: "sidecar", Image: "busybox", RestartPolicy: ptr.To(core.ContainerRestartPolicyAlways)}}
		}, want: "spec.initContainers[0].resources"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod := numaValidationPod(ptr.To[int32](0))
			if tc.edit != nil {
				tc.edit(pod)
			}
			errs := ValidatePodSpec(&pod.Spec, &pod.ObjectMeta, field.NewPath("spec"), PodValidationOptions{})
			if tc.want == "" && len(errs) != 0 {
				t.Fatalf("unexpected validation errors: %v", errs)
			}
			if tc.want != "" && !strings.Contains(fmt.Sprint(errs), tc.want) {
				t.Fatalf("errors %v do not contain %q", errs, tc.want)
			}
		})
	}
}

func TestPodNUMAPlacementUpdateValidation(t *testing.T) {
	oldPod := numaValidationPod(ptr.To[int32](0))
	oldPod.ResourceVersion = "1"
	for _, tc := range []struct {
		name string
		edit func(*core.Pod)
		want string
	}{
		{name: "unrelated label", edit: func(p *core.Pod) { p.Labels = map[string]string{"updated": "true"} }},
		{name: "change", edit: func(p *core.Pod) { p.Spec.NUMANode = ptr.To[int32](1) }, want: "numaNode is immutable"},
		{name: "remove", edit: func(p *core.Pod) { p.Spec.NUMANode = nil }, want: "numaNode is immutable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newPod := oldPod.DeepCopy()
			tc.edit(newPod)
			errs := ValidatePodUpdate(newPod, oldPod, PodValidationOptions{})
			if tc.want == "" && len(errs) != 0 {
				t.Fatalf("unexpected validation errors: %v", errs)
			}
			if tc.want != "" && !strings.Contains(fmt.Sprint(errs), tc.want) {
				t.Fatalf("errors %v do not contain %q", errs, tc.want)
			}
		})
	}
	oldPod.Spec.NUMANode = nil
	newPod := oldPod.DeepCopy()
	newPod.Spec.NUMANode = ptr.To[int32](0)
	if errs := ValidatePodUpdate(newPod, oldPod, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), "numaNode is immutable") {
		t.Fatalf("adding numaNode after creation: %v", errs)
	}
	if errs := ValidatePodStatusUpdate(newPod, oldPod, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), "numaNode is immutable") {
		t.Fatalf("adding numaNode through status update: %v", errs)
	}
}

func TestPodNUMAPlacementSubresources(t *testing.T) {
	oldPod := numaValidationPod(ptr.To[int32](0))
	oldPod.ResourceVersion = "1"
	newPod := oldPod.DeepCopy()
	newPod.Spec.Containers[0].Resources.Requests[core.ResourceCPU] = resource.MustParse("3")
	newPod.Spec.Containers[0].Resources.Limits[core.ResourceCPU] = resource.MustParse("3")
	if errs := ValidatePodResize(newPod, oldPod, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), "CPU or memory resize is not supported") {
		t.Fatalf("resize errors: %v", errs)
	}
	newPod = oldPod.DeepCopy()
	newPod.Spec.EphemeralContainers = []core.EphemeralContainer{{EphemeralContainerCommon: core.EphemeralContainerCommon{Name: "debug", Image: "busybox"}}}
	if errs := ValidatePodEphemeralContainersUpdate(newPod, oldPod, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), "ephemeral containers are not supported") {
		t.Fatalf("ephemeral container errors: %v", errs)
	}
}

func TestPodIndependentNUMAValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*core.Pod)
		want string
	}{
		{name: "distinct targets"},
		{name: "equal targets", edit: func(p *core.Pod) { p.Spec.MemNUMA = ptr.To[int32](0) }},
		{name: "maximum", edit: func(p *core.Pod) { p.Spec.CPUNUMA = ptr.To[int32](63); p.Spec.MemNUMA = ptr.To[int32](63) }},
		{name: "CPU negative", edit: func(p *core.Pod) { p.Spec.CPUNUMA = ptr.To[int32](-1) }, want: "spec.cpuNUMA"},
		{name: "memory too large", edit: func(p *core.Pod) { p.Spec.MemNUMA = ptr.To[int32](64) }, want: "spec.memNUMA"},
		{name: "legacy combination", edit: func(p *core.Pod) { p.Spec.NUMANode = ptr.To[int32](0) }, want: "cannot be combined"},
		{name: "fractional CPU", edit: func(p *core.Pod) {
			p.Spec.Containers[0].Resources.Requests[core.ResourceCPU] = resource.MustParse("1500m")
			p.Spec.Containers[0].Resources.Limits[core.ResourceCPU] = resource.MustParse("1500m")
		}, want: "positive whole number"},
		{name: "missing memory", edit: func(p *core.Pod) { delete(p.Spec.Containers[0].Resources.Limits, core.ResourceMemory) }, want: "memory request and limit"},
		{name: "CPU just below a whole number", edit: func(p *core.Pod) {
			p.Spec.Containers[0].Resources.Requests[core.ResourceCPU] = resource.MustParse("0.9999")
			p.Spec.Containers[0].Resources.Limits[core.ResourceCPU] = resource.MustParse("0.9999")
		}, want: "positive whole number"},
		{name: "pod budget", edit: func(p *core.Pod) { p.Spec.Resources = &core.ResourceRequirements{} }, want: "pod-level resources"},
		{name: "windows", edit: func(p *core.Pod) { p.Spec.OS = &core.PodOS{Name: core.Windows} }, want: "requires a Linux Pod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := numaValidationPod(nil)
			pod.Spec.CPUNUMA, pod.Spec.MemNUMA = ptr.To[int32](0), ptr.To[int32](3)
			if tc.edit != nil {
				tc.edit(pod)
			}
			errs := ValidatePodSpec(&pod.Spec, &pod.ObjectMeta, field.NewPath("spec"), PodValidationOptions{})
			if tc.want == "" && len(errs) != 0 || tc.want != "" && !strings.Contains(fmt.Sprint(errs), tc.want) {
				t.Fatalf("validation = %v, want %q", errs, tc.want)
			}
		})
	}
}

func TestPodIndependentNUMALifecycle(t *testing.T) {
	for _, fieldName := range []string{"cpuNUMA", "memNUMA"} {
		for _, change := range []string{"add", "remove", "change"} {
			t.Run(fieldName+"/"+change, func(t *testing.T) {
				old := numaValidationPod(nil)
				old.ResourceVersion = "1"
				old.Spec.CPUNUMA, old.Spec.MemNUMA = ptr.To[int32](0), ptr.To[int32](3)
				set := func(p *core.Pod, value *int32) {
					if fieldName == "cpuNUMA" {
						p.Spec.CPUNUMA = value
					} else {
						p.Spec.MemNUMA = value
					}
				}
				if change == "add" {
					set(old, nil)
				}
				next := old.DeepCopy()
				if change == "remove" {
					set(next, nil)
				} else {
					set(next, ptr.To[int32](2))
				}
				for _, validate := range []func(*core.Pod, *core.Pod, PodValidationOptions) field.ErrorList{ValidatePodUpdate, ValidatePodStatusUpdate, ValidatePodResize, ValidatePodEphemeralContainersUpdate} {
					if errs := validate(next, old, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), fieldName+" is immutable") {
						t.Fatalf("mutation accepted: %v", errs)
					}
				}
			})
		}
	}
	old := numaValidationPod(nil)
	old.ResourceVersion = "1"
	old.Spec.CPUNUMA, old.Spec.MemNUMA = ptr.To[int32](0), ptr.To[int32](3)
	next := old.DeepCopy()
	next.Labels = map[string]string{"updated": "true"}
	if errs := ValidatePodUpdate(next, old, PodValidationOptions{}); len(errs) != 0 {
		t.Fatal(errs)
	}
	next.Spec.Containers[0].Resources.Requests[core.ResourceMemory] = resource.MustParse("512Mi")
	next.Spec.Containers[0].Resources.Limits[core.ResourceMemory] = resource.MustParse("512Mi")
	if errs := ValidatePodResize(next, old, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), "CPU or memory resize is not supported") {
		t.Fatal(errs)
	}
	next = old.DeepCopy()
	next.Spec.EphemeralContainers = []core.EphemeralContainer{{EphemeralContainerCommon: core.EphemeralContainerCommon{Name: "debug", Image: "busybox"}}}
	if errs := ValidatePodEphemeralContainersUpdate(next, old, PodValidationOptions{}); !strings.Contains(fmt.Sprint(errs), "ephemeral containers are not supported") {
		t.Fatal(errs)
	}
}

func TestPodIndependentNUMAWholeCPUFormats(t *testing.T) {
	for _, amount := range []string{"1", "1000m", "1.0", "1.000000000000000000"} {
		t.Run(amount, func(t *testing.T) {
			pod := numaValidationPod(nil)
			pod.Spec.CPUNUMA, pod.Spec.MemNUMA = ptr.To[int32](0), ptr.To[int32](3)
			pod.Spec.Containers[0].Resources.Requests[core.ResourceCPU] = resource.MustParse(amount)
			pod.Spec.Containers[0].Resources.Limits[core.ResourceCPU] = resource.MustParse(amount)
			if errs := ValidatePodSpec(&pod.Spec, &pod.ObjectMeta, field.NewPath("spec"), PodValidationOptions{}); len(errs) != 0 {
				t.Fatal(errs)
			}
		})
	}
}
