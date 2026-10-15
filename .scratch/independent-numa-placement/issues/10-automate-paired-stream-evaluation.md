# 10: Automate paired STREAM evaluation and evidence reporting

**What to build:** An operator can run controlled paired memory-node experiments through the new Pod fields and receive bandwidth summaries together with CPU allocation and live array-residency evidence. Reports distinguish VM measurements from verified DDR/HBM comparisons.

**Blocked by:** 08 — Package a reproducible STREAM workload with residency evidence; 09 — Deliver a runnable VM and hardware placement-validation kit.

**Status:** resolved

**Type:** task

- [x] Provide paired manifests and an orchestrated run/report path using both explicit fields, the completed policy, the reproducible workload, and shared evidence collectors.
- [x] Hold actual CPU sets, thread count, binary/build flags, array size, and resource settings equal across paired runs. Reject or clearly flag mismatches rather than relying only on the CPU NUMA ID.
- [x] Size arrays to exceed the relevant cache and fit both memory targets and container limits; include a configurable one-thread/multi-thread sweep without oversubscription.
- [x] Collect array residency after initialization outside timed kernels, numeric validation, raw output/units, workload metadata, and actual effective masks.
- [x] Run warm-up and at least five measured executions per configuration; report median and range while retaining per-execution output. Interleave paired cases where practical and record interference/settings.
- [x] Do not claim a successful controlled comparison after numerical failure, OOM, wrong placement, missing residency evidence, or uncontrolled CPU-set mismatch.
- [x] Require verified backing memory types for DDR/HBM labels; virtual nodes backed by ordinary host memory remain VM measurements. Apply no required HBM speedup threshold.
- [x] Check orchestration/reporting on recorded or synthetic result fixtures and through feasible smoke execution; clearly mark fixtures and unavailable live runs.
- [x] Assemble milestone-1 readiness from actual implementation/tooling verification and outstanding runtime limitations. No external host or fabricated throughput is required to complete software/tooling delivery.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 10 of 11. This completes the software and tooling milestone after its blockers. Full live execution remains ticket 11.

## Answer

Resolved on 2026-10-15 on `numa_support_hbm`. Implementation commits:
`d1ee8ab825e`, `6d141ff5d8e`, `f39bebad16b`.

The [paired evaluation CLI](../evaluation/README.md) prepares explicit matched
manifests, executes sequential alternating warm-up/repeated pairs with a thread
sweep, captures cluster/workload/array evidence and reports decimal-MB/s median
and range. It rejects mismatching actual CPU/build/resource settings and
invalid numerical/placement/residency evidence. Neutral VM/hardware labels
become DDR/HBM labels only with retained operator-verified backing evidence.

All 16 evaluation, 14 STREAM and 12 runtime CLI tests pass (42 total), including
synthetic cluster orchestration/cleanup and actual native serial STREAM smoke.
Python typechecking, boilerplate checks and diff checks pass. Final independent
Standards and Spec reviews have zero remaining findings after reproducing and
fixing uncertain create cleanup and API quantity canonicalization.

[Verification](../verification-10.md) and [milestone-1 readiness](../milestone-1.md)
record actual checks and earlier Kubernetes-suite limitations. Linux/OpenMP
container/cluster runs, live array residency and VM/DDR/HBM measurements remain
NOT RUN for ticket 11. No throughput or hardware observation was fabricated.
