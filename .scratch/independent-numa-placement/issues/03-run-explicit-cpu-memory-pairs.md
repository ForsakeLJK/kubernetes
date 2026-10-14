# 03: Run one application container with explicit CPU and memory targets

**What to build:** A Pod with one application container can specify both cpuNUMA and memNUMA and execute with CPUs from the selected CPU node and ordinary memory confined to the selected memory node, including a memory-only node. The guarantee survives restarts and failed allocations.

**Blocked by:** 02 — Enable independent-numa policy for existing Pod forms.

**Status:** resolved

**Type:** task

- [x] Add both optional API fields through serialization/conversion and generated artifacts. Preserve absence versus zero, accept equal targets, and validate IDs from 0 through 63.
- [x] Reject legacy/new-field combinations even when values agree. Retain Guaranteed equal CPU/memory requests and limits, positive whole CPUs, immutable fields, and existing resize, ephemeral-container, and Pod-budget exclusions.
- [x] Require independent-numa for new-field Pods. Temporarily refuse partial requests, multiple/init/sidecar containers, and hugepage requests with explicit unsupported-slice diagnostics until their follow-up tickets land.
- [x] Validate actual sparse topology and resource availability; accept CPU-less memory targets but reject CPU-less CPU targets. Explicit pairs need no distance ranking.
- [x] Coordinate CPU and device hints together and ordinary memory independently. Preserve device preference/rejection and allocation behavior without claiming new hard device locality.
- [x] Resolve and store separate singleton targets, allocate concrete resources on them, validate before container start, and pass separate CPU and memory masks to runtime. Never use a combined two-node mask.
- [x] Refuse shortages even when another node has capacity. Roll back newly acquired CPU, memory, and device allocations on partial failures; preserve unrelated/restored assignments.
- [x] Reconstruct and validate matching targets after container/kubelet restart; reject conflicts without migration, mask broadening, checkpoint repair, or duplicate reservations.
- [x] Log the target pair and count split admissions accurately, without incrementing global common-node alignment success.
- [x] Verify API-to-runtime behavior for distinct/equal targets, sparse IDs, CPU-less memory, device conflicts, failure cleanup, and recovery using synthetic topology. Include ordinary/legacy regression and required Linux build checks.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 03 of 11. This is the first externally usable split-placement slice. Temporary refusals restrict unsupported workloads, not the guarantees for admitted workloads. API presence alone never authorizes a container to start.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14. One application container can
require distinct or equal CPU/memory NUMA targets, including CPU-less memory
nodes. Both optional API fields preserve absence versus zero through generated
serialization, conversion, deepcopy, schema, and apply artifacts. Eligibility,
immutability, and lifecycle restrictions apply to both fields; unsupported
partial, multi-container/init/sidecar, and hugepage forms fail closed.

CPU/device hints coordinate independently of memory feasibility. Concrete
allocations and separate singleton runtime masks are validated; shortages do
not fall back elsewhere. Transaction cleanup preserves unrelated and restored
assignments. Repeated and partial checkpoint restoration retain the requested
pair without duplicate reservations, migration, mask broadening, or repair.
Split admissions are logged and counted separately from common-node alignment.

Focused race tests, actual-manager allocation/checkpoint/mask tests, all 11
platform typechecks, Linux cross-compilation, vet, generators, formatting,
boilerplate, and metric checks pass. Standards and Spec reviews have zero
remaining findings after correcting validator names and whole-CPU quantity
validation. The full suite reports 145 failures in 11 previously recorded
package groups; every failure name occurs in ticket 01 or 02. Ancillary suites
pass. No Linux VM/hardware runtime execution is claimed.
See [verification-03.md](../verification-03.md) for evidence and limitations.
