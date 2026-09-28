# 02: Enforce CPU and memory placement for a single-container Pod

**What to build:** A supported Pod with one application container and ordinary memory runs with CPU affinity and memory-allocation confinement on its requested NUMA node. If the node cannot satisfy the requirement, admission fails even when another node has spare resources.

**Blocked by:** 01 — Introduce and validate the NUMA placement requirement.

**Status:** ready-for-agent

**Type:** task

- [ ] Enable admission for supported single-application-container Pods without init containers, sidecars, hugepage requests, or restored assignments, replacing the corresponding temporary refusal from ticket 01.
- [ ] Select the requested node even when another node would otherwise be preferred. All assigned CPUs must belong to that node and the runtime memory-node mask must contain exactly that node.
- [ ] Enforce the hard requirement through feasibility checking, concrete CPU/memory allocation, and runtime container configuration. A selected topology hint alone is insufficient.
- [ ] Refuse CPU or ordinary-memory shortages on the requested node without CPU fallback or memory-mask expansion. Existing policy options must not override the requirement.
- [ ] Preserve existing device topology-policy decisions and diagnostics. Device hints must never weaken CPU or memory confinement; introduce no additional device-locality guarantee.
- [ ] Report the requested ID and relevant resource shortage through existing status/events. Do not introduce automatic relocation or a custom retry mechanism.
- [ ] Release allocations newly acquired by an unsuccessful attempt, including failure between CPU and memory allocation. Normal cleanup also releases resources; do not remove pre-existing assignments.
- [ ] Keep not-yet-supported multi-container, init/sidecar, hugepage, and restored-assignment cases explicitly refused until their tickets land. Unsupported recovery must not silently start a container with weaker placement.
- [ ] Test requested-node selection, spare capacity elsewhere, CPU and memory shortages, provider/device conflicts, runtime masks, and injected partial allocation failures using synthetic multi-NUMA topology.
- [ ] Verify omitted-field behavior remains unchanged across the allocation paths and policies touched by this change. Document the guarantee as affinity and allocation confinement, not physical locality of every shared or cached page.

## Comments

Approved ticket breakdown: slice 02 of 06. Tickets 03 and 04 can start independently when this ticket is resolved.
