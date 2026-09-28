# 01: Introduce and validate the NUMA placement requirement

**What to build:** A Pod can declare an optional, immutable NUMA placement requirement that is preserved by the API. Invalid declarations and unsupported node configurations produce actionable errors. Until allocation support is implemented, the kubelet refuses participating Pods rather than silently ignoring their requirement.

**Blocked by:** None (can start immediately).

**Status:** claimed

**Type:** task

- [ ] Introduce the optional integer PodSpec field `numaNode`, preserving omission independently of zero through supported API serialization, conversion, copying, and client operations. Regenerate affected artifacts using repository tooling.
- [ ] Validate IDs from 0 through 63 inclusive. Reject changes, additions, and removals of the field after Pod creation.
- [ ] Require Guaranteed QoS, CPU and memory requests equal to their respective limits for every application container, init container, and sidecar, and positive whole-number CPU quantities. Reject participating Pods using Pod-level resource budgets.
- [ ] Reject in-place CPU/memory resize and ephemeral-container additions for participating Pods regardless of other feature gates. Continue to permit otherwise valid unrelated updates.
- [ ] Check Linux, CPU Manager `static`, Memory Manager `Static`, and Topology Manager `single-numa-node` with `pod` scope. Distinguish unsupported configuration from a nonexistent local NUMA ID using existing status/events.
- [ ] Check actual topology membership, including synthetic noncontiguous NUMA IDs; do not infer membership from the node count.
- [ ] Until subsequent slices enable allocation, refuse otherwise valid participating Pods with a clear implementation-not-yet-supported error and no resource allocation. This is temporary staging behavior, not the final feature contract.
- [ ] Add automated API round-trip, create/update/subresource validation, and kubelet refusal tests, including zero, omission, range boundaries, invalid workloads, and unsupported configurations.
- [ ] Pods omitting the field retain existing validation and kubelet behavior. Introduce no feature gate, scheduler change, or new placement-status API.

## Comments

Approved ticket breakdown: slice 01 of 06. All implementation work remains pending.
