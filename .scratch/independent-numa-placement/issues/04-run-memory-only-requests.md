# 04: Run memory-only requests with automatic CPU placement

**What to build:** A single-application-container Pod can specify memNUMA alone, including a CPU-less memory node, and receive one feasible CPU node chosen by the approved locality rules. Its automatic CPU target is preserved across restarts.

**Blocked by:** 03 — Run one application container with explicit CPU and memory targets.

**Status:** resolved

**Type:** task

- [x] Remove the temporary memory-only refusal while retaining unsupported-shape restrictions owned by later tickets.
- [x] Keep the memory target mandatory and evaluate CPU candidates with compatible device hints and sufficient singleton CPU capacity.
- [x] Prefer a valid restored CPU target, then a feasible common node, then the closest feasible CPU-to-memory pair. Break equal known distances by ascending candidate ID.
- [x] Load and validate topology distances independently of the older optional closest-node flag. Decode actual source column ordering; cover sparse IDs and asymmetric distances without raw-ID indexing into compact rows.
- [x] Refuse when multiple nonlocal candidates cannot be ranked because relevant distances are unusable or missing. Do not discard unknown-distance candidates or silently select by ID.
- [x] Avoid unnecessary distance requirements for a restored target, a feasible common node, or a sole feasible candidate. Diagnose absent candidates as capacity/topology failure.
- [x] Keep automatic CPUs within one node even when aggregate multi-node capacity would satisfy the Pod. Enforce the resolved CPU target during allocation and runtime configuration.
- [x] Preserve the automatically chosen target through matching recovery; reject conflicting restored assignments and roll back both resources on failed admission.
- [x] Test complete requests through allocation, runtime masks, cleanup, and recovery for every selection/error branch, including CPU-less memory and device conflicts.
- [x] Factor candidate ranking so CPU-only placement can reuse the same directed CPU-to-memory semantics in ticket 05; record verification evidence.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 04 of 11. Locality is automatic selection among feasible placements, not a relaxation of memory confinement. This ticket supplies the shared validated ranking behavior used by ticket 05.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14. One application container can
specify `memNUMA` alone, including CPU-less memory nodes. The automatic CPU
selection uses singleton resource/device feasibility, preserves valid restored
targets, prefers a feasible common node, then ranks directed CPU-to-memory
distances with ascending-ID ties. Sparse IDs and shuffled discovery order map
to the producer's compact columns. Unknown/malformed relevant distances refuse
ambiguous ranking; restored, common, and sole candidates need no comparison.

The selected CPU target is mandatory during real allocation and CRI mask
construction. Recovery preserves it despite improved locality or unavailable
distances; partial restoration, conflicting checkpoints, normal cleanup, and
transaction rollback are covered. Device preference/rejection remains intact.
CPU-only, multi-container/init/sidecar, and hugepage forms retain their later
slice refusals. Pair ranking is shared for ticket 05.

Focused race tests, all 11 platform typechecks, Linux cross-compilation, vet,
formatting, and boilerplate pass. Standards and Spec reviews report zero
findings. The full suite reports 145 failures in 11 package groups, with every
failure name present in earlier ticket records; ancillary suites pass. See
[verification-04.md](../verification-04.md) for commands, evidence, and limits.
No Linux VM/hardware runtime or performance result is claimed.
