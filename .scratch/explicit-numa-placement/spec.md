# Explicit Pod NUMA placement

Status: proposed for final review; design interview complete, implementation not started.

## Purpose

Allow a Pod in this private Kubernetes research fork to require CPU and memory placement on one explicitly identified NUMA node. If the kubelet cannot satisfy the requirement, it must refuse admission instead of placing the Pod on another NUMA node.

The feature originates from a research-work request; no concrete application workload or performance target has been identified. Acceptance therefore measures placement correctness, failure behavior, and compatibility, not application speedup. Upstream acceptance is outside the current scope.

The agreed vocabulary is in [CONTEXT.md](../../CONTEXT.md). The interview record is in [map.md](map.md).

## API contract

Add an optional integer field to PodSpec, with the proposed name `numaNode`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: numa-placement-example
spec:
  numaNode: 1
  containers:
    - name: workload
      image: busybox:1.36
      command: ["sh", "-c", "sleep 3600"]
      resources:
        requests:
          cpu: "2"
          memory: 256Mi
        limits:
          cpu: "2"
          memory: 256Mi
```

- Omission preserves existing Kubernetes behavior.
- Zero explicitly selects NUMA node 0; it must not be confused with omission.
- Valid values are integers from 0 through 63, inclusive, matching the current Topology Manager mask representation.
- The ID identifies a NUMA node on the machine selected for the Pod. It is not a cluster-wide identifier.
- The kubelet must verify membership in its actual NUMA topology. IDs need not be contiguous; checking against the number of NUMA nodes is insufficient.
- The field is immutable after Pod creation, including adding or removing it. A different requirement needs a replacement Pod.
- No additional feature gate is introduced. Field presence opts a Pod into the feature.

The example illustrates the API, not machine selection. The operator must separately arrange scheduling to a suitable machine.

## Supported configuration and workloads

A participating Pod must run on Linux with all of these kubelet settings:

| Setting | Required value |
| --- | --- |
| CPU Manager policy | `static` |
| Memory Manager policy | `Static` |
| Topology Manager policy | `single-numa-node` |
| Topology Manager scope | `pod` |

The kubelet and its resource managers must otherwise be validly configured and able to enforce CPU and memory-node masks through the container runtime.

Participating Pods must have Guaranteed QoS. Every application container, init container, and sidecar must declare CPU and memory requests and limits, with each request equal to its corresponding limit. Each CPU quantity must be a positive whole number. Existing Kubernetes resource validation continues to apply.

Participating Pods using Pod-level resource budgets (`spec.resources`) are rejected in this version. The requirement is Pod-wide, but allocation follows the supported per-container resource declaration path.

Unsupported kubelet configurations must cause refusal of participating Pods. They must not change the behavior of Pods that omit the field.

## Placement guarantee

For requested NUMA node N:

1. Every assigned CPU for an application container, init container, or sidecar must belong to N.
2. Every such container's allowed memory-node mask must contain exactly N.
3. Ordinary memory and every requested hugepage size must be allocated/accounted for on N by the resource managers.
4. No CPU fallback, memory-mask expansion, or allocation of requested hugepages on another NUMA node is permitted to satisfy the request.
5. If any covered allocation cannot meet these conditions, the kubelet must refuse admission.

Resource feasibility must retain Kubernetes' existing handling of init-container sequencing, restartable init containers, concurrent application containers, and resource reuse. It must not assume all sequential init containers run simultaneously or overlook sidecar overlap.

This is a CPU-affinity and memory-allocation-confinement guarantee. It is not a guarantee that every physical page accessible to a process, including shared pages or previously allocated file-cache pages, resides on N. It adds no page migration mechanism.

Device handling remains governed by existing topology policies. This feature adds no device-locality guarantee and does not bypass existing device-related rejection. Device hints must never cause the CPU or memory requirement to be weakened.

## Admission and failure handling

The API validates constraints that can be established from the Pod, including field range, resource declarations, prohibited operations, and immutability. The kubelet validates node-local facts and placement feasibility. API validation alone must not be treated as evidence that local placement is feasible.

The scheduler is unchanged. A Pod can be assigned to a machine that cannot satisfy the request and subsequently be refused by the kubelet. The feature introduces no automatic relocation, scheduler reservation, or custom retry mechanism.

Use existing Pod status and event mechanisms. Errors must distinguish at least:

- Unsupported kubelet configuration.
- Requested NUMA ID absent from the machine.
- Insufficient eligible CPUs, ordinary memory, or requested hugepage capacity on that node.
- Incompatible restored resource assignments.

Messages should identify the requested ID and the relevant configuration or resource failure. Existing device/topology errors remain applicable. A new placement-status API is unnecessary.

An unsuccessful allocation attempt must release resources it newly acquired, including partial allocations for earlier containers or resource managers. Cleanup must preserve valid pre-existing assignments, including restored assignments. No admission failure may leave leaked reservations or authorize starting a covered container outside the requirement.

## Lifecycle

### Container and kubelet restarts

Container restarts retain the same NUMA requirement. Kubelet recovery must validate restored CPU and memory assignments against it and reuse valid assignments.

If restored assignments conflict, block new admission or affected container starts/restarts and report an actionable error. Do not silently relocate allocations, broaden masks, or repair checkpoints automatically. This feature does not introduce automatic eviction of already running containers or replace existing handling of corrupt manager checkpoints.

### Updates and debugging

- Reject in-place CPU or memory resize for participating Pods, even if other resize feature gates are enabled. Resource changes require Pod replacement.
- Reject additions of ephemeral containers to participating Pods. They are outside the supported allocation path.
- Continue to permit otherwise valid, unrelated Pod updates under existing Kubernetes rules.

## Compatibility and deployment boundary

Pods omitting `numaNode` must retain existing validation, allocation, scheduling, lifecycle, and failure behavior. Changes to shared allocation code must preserve that property.

The guarantee requires the modified API server and kubelet from this fork. Compatibility with old or unmodified components that cannot enforce the new field is outside the supported deployment. This version introduces neither capability negotiation nor scheduler-based selection of feature-capable kubelets.

## Implementation constraints and code grounding

Inspected baseline: `68f9fc030c6`. These are implementation entry points, not a requirement to use one particular algorithm.

- External/internal PodSpec declarations: `staging/src/k8s.io/api/core/v1/types.go` and `pkg/apis/core/types.go`. Preserve field presence independently of integer value.
- Pod and update validation: `pkg/apis/core/validation/validation.go`, including resize and ephemeral-container subresources.
- Pod-wide topology admission: `pkg/kubelet/cm/topologymanager/scope_pod.go`.
- CPU placement: `pkg/kubelet/cm/cpumanager/policy_static.go`. The existing aligned-then-fallback allocation path is insufficient for a hard requirement.
- Memory placement: `pkg/kubelet/cm/memorymanager/policy_static.go`. Existing mask expansion must not weaken the requirement.
- Runtime masks: `pkg/kubelet/cm/internal_container_lifecycle_linux.go` and `pkg/kubelet/cm/memorymanager/memory_manager.go`.
- Resize handling: `pkg/kubelet/allocation/handlers.go`.
- Restoration: CPU and Memory Manager checkpoints preserve concrete assignments; Topology Manager hints alone do not establish valid recovered placement.

A Topology Manager hint alone is not an enforcement boundary. Implementation must maintain the requirement through feasibility checks, concrete allocation, restoration, and the runtime configuration passed to containers.

Regenerate affected API artifacts with repository tooling. Do not manually edit generated deepcopy, protobuf, conversion, OpenAPI, or apply-configuration outputs.

## Acceptance criteria

### Automated tests without a physical multi-NUMA host

- Round-trip the API field and distinguish omission from zero.
- Validate range boundaries, negative/out-of-range values, immutable changes, and disallowed workload declarations.
- Reject unsupported kubelet configurations only for participating Pods.
- Test nonexistent and noncontiguous NUMA IDs using synthetic topology.
- With capacity available on both nodes, select the requested node rather than a provider's otherwise preferred node.
- Refuse when the requested node lacks CPUs, memory, or a requested hugepage size, even if another node has capacity.
- Exercise application, init, and sidecar resource accounting and allocation.
- Preserve existing device-policy decisions without allowing CPU/memory fallback.
- Reuse matching restored assignments and refuse conflicting assignments.
- Reject resize and ephemeral-container additions; preserve unrelated valid updates.
- Inject partial allocation failures and verify cleanup without removing valid existing assignments.
- Verify omitted-field regression cases across policies and lifecycle operations touched by the change.

### Linux runtime acceptance

Use a machine exposing at least two NUMA nodes, with the required kubelet configuration and enough reserved/allocatable ordinary memory and hugepages for the cases being tested. Use the fork's API server, kubelet, and a runtime that enforces the masks.

- Run successful Pods selecting NUMA 0 and another existing node.
- Verify actual container CPU masks contain only CPUs from the requested node and actual allowed memory-node masks contain exactly that node; API admission or selected hints alone are insufficient evidence.
- Include application containers, init containers, and sidecars; inspect init containers while they are running.
- Repeat mask checks after container restart and kubelet restart.
- Exercise CPU, ordinary-memory, and hugepage exhaustion on the requested node while another node has spare capacity; verify refusal.
- Verify no leaked allocations after failed attempts or normal Pod cleanup.
- Run equivalent omitted-field Pods to verify existing behavior.

The eventual implementation report must separate automated results from host-dependent results and identify any runtime acceptance tests that could not run. No tests or performance measurements have been executed during this design-only task.

## Explicit exclusions

Soft preferences or fallback; multiple allowed NUMA nodes; per-container NUMA IDs; Windows; shared-CPU workloads; alternative kubelet policy combinations; Pod-level resource budgets; live NUMA migration; in-place resizing; ephemeral debug containers; stronger device-locality guarantees; scheduler awareness; physical locality of every accessible page; and upstream enhancement-proposal work.
