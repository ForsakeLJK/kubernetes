# 03: Extend placement to application containers, init containers, and sidecars

**What to build:** All application containers, init containers, and sidecars in a participating Pod share the requested NUMA node, with correct resource accounting and complete cleanup when admission fails partway through the Pod.

**Blocked by:** 02 — Enforce CPU and memory placement for a single-container Pod.

**Status:** claimed

**Type:** task

- [ ] Enable Pod-wide placement for multiple application containers, ordinary init containers, and restartable init containers/sidecars; remove the corresponding temporary restrictions.
- [ ] Every covered container receives CPUs belonging to the requested node and a memory-node mask containing exactly that node.
- [ ] Preserve existing resource accounting and reuse for sequential init containers, concurrent application containers, and overlapping sidecars. Do not sum sequential init-container requests as though they all run concurrently.
- [ ] Refuse a Pod when its required concurrent resources cannot fit on the requested node, even when its containers could individually fit or another node has spare resources.
- [ ] On failure in a later container or resource manager, release all allocations newly acquired by that attempt across the Pod. Preserve valid pre-existing assignments and prohibit partial admission from starting containers outside the requirement.
- [ ] Test successful multi-container placement, sequential init resource reuse, sidecar overlap, insufficient combined capacity, and failures injected at successive allocation stages.
- [ ] Test the runtime configuration produced for every covered container and resource release after normal Pod cleanup.
- [ ] Retain hugepage and restored-assignment restrictions only until their respective supporting tickets land. This ticket must be independently implementable without ticket 04.
- [ ] Preserve existing omitted-field behavior for multi-container Pods, init containers, sidecars, and resource reuse.

## Comments

Approved ticket breakdown: slice 03 of 06. Can proceed independently of ticket 04.
