# 10: Automate paired STREAM evaluation and evidence reporting

**What to build:** An operator can run controlled paired memory-node experiments through the new Pod fields and receive bandwidth summaries together with CPU allocation and live array-residency evidence. Reports distinguish VM measurements from verified DDR/HBM comparisons.

**Blocked by:** 08 — Package a reproducible STREAM workload with residency evidence; 09 — Deliver a runnable VM and hardware placement-validation kit.

**Status:** claimed

**Type:** task

- [ ] Provide paired manifests and an orchestrated run/report path using both explicit fields, the completed policy, the reproducible workload, and shared evidence collectors.
- [ ] Hold actual CPU sets, thread count, binary/build flags, array size, and resource settings equal across paired runs. Reject or clearly flag mismatches rather than relying only on the CPU NUMA ID.
- [ ] Size arrays to exceed the relevant cache and fit both memory targets and container limits; include a configurable one-thread/multi-thread sweep without oversubscription.
- [ ] Collect array residency after initialization outside timed kernels, numeric validation, raw output/units, workload metadata, and actual effective masks.
- [ ] Run warm-up and at least five measured executions per configuration; report median and range while retaining per-execution output. Interleave paired cases where practical and record interference/settings.
- [ ] Do not claim a successful controlled comparison after numerical failure, OOM, wrong placement, missing residency evidence, or uncontrolled CPU-set mismatch.
- [ ] Require verified backing memory types for DDR/HBM labels; virtual nodes backed by ordinary host memory remain VM measurements. Apply no required HBM speedup threshold.
- [ ] Check orchestration/reporting on recorded or synthetic result fixtures and through feasible smoke execution; clearly mark fixtures and unavailable live runs.
- [ ] Assemble milestone-1 readiness from actual implementation/tooling verification and outstanding runtime limitations. No external host or fabricated throughput is required to complete software/tooling delivery.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 10 of 11. This completes the software and tooling milestone after its blockers. Full live execution remains ticket 11.
