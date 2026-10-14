# 09: Deliver a runnable VM and hardware placement-validation kit

**What to build:** An operator can deploy the completed feature on a suitable Linux VM or host, run its placement/lifecycle/refusal matrix, and collect an honest case-by-case evidence report without needing to design the checks themselves.

**Blocked by:** 07 — Confine ordinary memory and requested hugepages to the memory target.

**Status:** claimed

**Type:** task

- [ ] Provide fixtures and configurable inputs for explicit pairs, each partial request, equal-node pairs, legacy requests, and ordinary Pods using actual machine IDs and capacities.
- [ ] Verify separate actual CPU and memory-node masks for running application, init, and sidecar containers. Preserve legacy checker/fixture compatibility.
- [ ] Include actual touched-memory residency observation and hugepage checks; do not treat an allowed mask, successful API response, or chosen hint as page-residency proof.
- [ ] Document matched fork components, policy/scope, CPU/memory reservations, sparse topology, and memory-only nodes without assuming every memory node has CPUs.
- [ ] Include container/kubelet restart, post-recovery starts, target exhaustion with spare capacity elsewhere, invalid configurations/updates, device conflicts where available, and leak/cleanup checks.
- [ ] Capture commit, manifests, image/runtime/kernel versions, topology/distances, commands, masks, residency, and PASS/FAIL/NOT RUN per case. Missing prerequisites are not passes.
- [ ] Test checker/report handling with representative valid/invalid output, including sparse masks and incomplete evidence. Provide a complete operator procedure; execution on a live VM/host belongs to ticket 11.
- [ ] Preserve historical results and acknowledge the user's earlier manual VM testing without inventing its commands or outputs.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 09 of 11. Ticket 07 gates the full matrix: the kit must describe the completed feature rather than intermediate unsupported forms. No HBM host is required to finish this tooling ticket.
