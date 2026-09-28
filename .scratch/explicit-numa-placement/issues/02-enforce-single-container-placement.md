# 02: Enforce CPU and memory placement for a single-container Pod

**What to build:** A supported Pod with one application container and ordinary memory runs with CPU affinity and memory-allocation confinement on its requested NUMA node. If the node cannot satisfy the requirement, admission fails even when another node has spare resources.

**Blocked by:** 01 — Introduce and validate the NUMA placement requirement.

**Status:** resolved

**Type:** task

- [x] Enable admission for supported single-application-container Pods without init containers, sidecars, hugepage requests, or restored assignments, replacing the corresponding temporary refusal from ticket 01.
- [x] Select the requested node even when another node would otherwise be preferred. All assigned CPUs must belong to that node and the runtime memory-node mask must contain exactly that node.
- [x] Enforce the hard requirement through feasibility checking, concrete CPU/memory allocation, and runtime container configuration. A selected topology hint alone is insufficient.
- [x] Refuse CPU or ordinary-memory shortages on the requested node without CPU fallback or memory-mask expansion. Existing policy options must not override the requirement.
- [x] Preserve existing device topology-policy decisions and diagnostics. Device hints must never weaken CPU or memory confinement; introduce no additional device-locality guarantee.
- [x] Report the requested ID and relevant resource shortage through existing status/events. Do not introduce automatic relocation or a custom retry mechanism.
- [x] Release allocations newly acquired by an unsuccessful attempt, including failure between CPU and memory allocation. Normal cleanup also releases resources; do not remove pre-existing assignments.
- [x] Keep not-yet-supported multi-container, init/sidecar, hugepage, and restored-assignment cases explicitly refused until their tickets land. Unsupported recovery must not silently start a container with weaker placement.
- [x] Test requested-node selection, spare capacity elsewhere, CPU and memory shortages, provider/device conflicts, runtime masks, and injected partial allocation failures using synthetic multi-NUMA topology.
- [x] Verify omitted-field behavior remains unchanged across the allocation paths and policies touched by this change. Document the guarantee as affinity and allocation confinement, not physical locality of every shared or cached page.

## Comments

Approved ticket breakdown: slice 02 of 06. Tickets 03 and 04 can start independently when this ticket is resolved.

## Answer

Single-container Pods with ordinary memory now select their requested NUMA node during pod-scope topology admission. Static CPU and Memory Managers allocate only on that node, and Linux container creation requires validated CPU and memory assignments before setting the CRI masks. Resource and device conflicts use existing admission status and events. A failed attempt releases newly acquired CPU, memory, and device records while retaining pre-existing assignments. Multi-container, init, hugepage, and restored-assignment paths remain explicitly refused for later tickets. The guarantee is CPU affinity and memory-allocation confinement, not physical locality of every shared or cached page.

Automated tests use synthetic multi-NUMA topology for selection, resource shortages, provider conflicts, concrete CPU rollback after an injected memory failure, runtime masks, restored-assignment refusal, and omitted-field behavior. Focused tests and the complete touched-package suites pass. Linux runtime acceptance on a physical multi-NUMA host could not run on this macOS machine.

The repository-wide `make test` race run completed its main phase with 160,560 tests, 416 skips, and 146 failures, then exited nonzero after its tool-package checks passed. The CPU Manager, Memory Manager, Topology Manager, and container-manager packages passed in that run. Failures included macOS-sensitive kubelet, cgroup, volume, and API test packages; the Device Manager race-package run also failed. A separate race rerun could not be built because the sandbox could not resolve `proxy.golang.org` to fetch a Go module, so the Device Manager race failure remains unclassified. This suite result is not a ticket acceptance pass.
