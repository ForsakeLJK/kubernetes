# 09: Deliver a runnable VM and hardware placement-validation kit

**What to build:** An operator can deploy the completed feature on a suitable Linux VM or host, run its placement/lifecycle/refusal matrix, and collect an honest case-by-case evidence report without needing to design the checks themselves.

**Blocked by:** 07 — Confine ordinary memory and requested hugepages to the memory target.

**Status:** resolved

**Type:** task

- [x] Provide fixtures and configurable inputs for explicit pairs, each partial request, equal-node pairs, legacy requests, and ordinary Pods using actual machine IDs and capacities.
- [x] Verify separate actual CPU and memory-node masks for running application, init, and sidecar containers. Preserve legacy checker/fixture compatibility.
- [x] Include actual touched-memory residency observation and hugepage checks; do not treat an allowed mask, successful API response, or chosen hint as page-residency proof.
- [x] Document matched fork components, policy/scope, CPU/memory reservations, sparse topology, and memory-only nodes without assuming every memory node has CPUs.
- [x] Include container/kubelet restart, post-recovery starts, target exhaustion with spare capacity elsewhere, invalid configurations/updates, device conflicts where available, and leak/cleanup checks.
- [x] Capture commit, manifests, image/runtime/kernel versions, topology/distances, commands, masks, residency, and PASS/FAIL/NOT RUN per case. Missing prerequisites are not passes.
- [x] Test checker/report handling with representative valid/invalid output, including sparse masks and incomplete evidence. Provide a complete operator procedure; execution on a live VM/host belongs to ticket 11.
- [x] Preserve historical results and acknowledge the user's earlier manual VM testing without inventing its commands or outputs.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 09 of 11. Ticket 07 gates the full matrix: the kit must describe the completed feature rather than intermediate unsupported forms. No HBM host is required to finish this tooling ticket.


## Answer

Resolved on 2026-10-14 on `numa_support_hbm`. Source commits `8097aa017ca` and
`a85bc8ad162` deliver the [runtime validation kit](../runtime/README.md):
machine-specific fixtures for all placement forms, gated init/application/
sidecar workloads, separate effective masks with legacy CLI compatibility,
touched anonymous/multi-size hugepage queries, immutable command captures,
and a full evidence checklist/report. Missing prerequisites and incomplete
observations cannot produce a PASS. Matched-component setup, sparse and
memory-only topology, reservations, recovery, exhaustion, API/config refusals,
devices and cleanup are covered by the operator procedure.

All 12 runtime CLI tests and 14 STREAM regression tests pass, as do Python
source typechecks, native C warning checks, boilerplate and diff checks. Review
found and corrected three reporting defects with red/green CLI regressions;
final independent Standards and Spec reviews each report zero remaining
findings. [verification-09.md](../verification-09.md) records commands, actual
outcomes and limitations. Native memory touching/artifact writing succeeds;
Linux page queries correctly remain NOT RUN on macOS. No Linux VM/hardware,
actual placement/residency or performance result is inferred. Ticket11 owns
live execution; historical results and the earlier manual VM-test report are
preserved without fabricated commands or outputs.
