# 07: Confine ordinary memory and requested hugepages to the memory target

**What to build:** Independent-placement Pods can request hugepages, with ordinary memory and every requested hugepage size confined to the same explicit or automatically selected memory node across all covered containers and restarts.

**Blocked by:** 06 — Enforce one placement pair across application, init, and sidecar containers.

**Status:** resolved

**Type:** task

- [x] Remove the temporary hugepage refusal after enforcing ordinary memory and all requested sizes as one memory group.
- [x] For automatic memory placement, rank only nodes feasible for the entire memory group. Never choose separate nodes for ordinary memory and different hugepage sizes.
- [x] For explicit memory placement, refuse missing page-size capacity even if other nodes have spare hugepages; preserve exact memory-mask confinement.
- [x] Apply full app/init/sidecar lifetime accounting, reuse, rollback, and checkpoint validation to every requested memory resource.
- [x] Verify matching/conflicting recovery, partial failure at multiple container/provider boundaries, and no leaks or duplicate reservations.
- [x] Exercise explicit, CPU-only, and memory-only forms on sparse and memory-only topology, along with device interaction and ordinary/legacy regressions.
- [x] Audit every temporary unsupported-slice guard introduced by tickets 03–06: remove those now covered; retain final contract exclusions.
- [x] Run the focused complete feature acceptance matrix and relevant repository verification/generator checks. Record commands, actual outcomes, and environment limitations; cross-compilation does not count as runtime execution.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 07 of 11. This completes source-level behavior for the approved workload model; runtime and performance evidence remain separate deliverables.

## Answer

Implemented on `numa_support_hbm` on 2026-10-14, source commit `b9fba7e9386`.
Explicit, CPU-only, and memory-only forms now confine ordinary memory and every
requested hugepage size to one memory target across applications, sequential
inits, and sidecars. Automatic placement ranks only whole-group-feasible nodes;
explicit placement refuses any missing or insufficient page size without fallback.
The temporary hugepage gate is removed; final workload/policy exclusions remain.

Per-resource lifetime accounting, init reuse, transactional rollback, checkpoint
consistency, automatic-target preservation, and exact runtime masks cover both
2Mi and 1Gi pages. Review found and corrected unfinished-init under-reservation
after partial recovery and an application restart-policy classification error.
Surviving blocks remain intact; missing allocations preserve the effective peak,
and final group validation rolls back failures across all containers/providers.

Focused acceptance and race suites, API/configuration regressions, all 11
platform typechecks, Linux compilation, vet, generated deepcopy/conversion
verification, formatting, and boilerplate pass. Final independent Standards and
Spec reviews report zero remaining findings, including three independent repros
and 80 heterogeneous lifetime/partial-checkpoint cases.

The full suite reports 162,515 tests, 417 skipped, and 143 failures in 11 groups;
all failure identities appear in tickets 01–06. The recurring device endpoint
watcher case passes in isolation. Ancillary suites pass. The repository-wide
check remains failed; no baseline failure is claimed repaired. See
[verification-07.md](../verification-07.md) for commands, actual outcomes,
review evidence, and environment limits. No Linux VM/hardware runtime, physical
residency, or performance evidence is claimed.
