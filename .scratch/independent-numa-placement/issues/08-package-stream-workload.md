# 08: Package a reproducible STREAM workload with residency evidence

**What to build:** An operator can build and run a configurable STREAM container, obtain validated bandwidth results, and identify the live initialized arrays for memory-residency observation. This workload is usable before the new Kubernetes feature is implemented.

**Blocked by:** None (can start immediately).

**Status:** resolved

**Type:** task

- [x] Pin official STREAM source by revision/checksum and record compiler, optimization/OpenMP flags, source license requirements, and image/build identity.
- [x] Support configurable array size and thread count, document resource sizing and cache-sizing requirements, and distinguish small smoke tests from memory-bandwidth experiments.
- [x] Preserve Copy, Scale, Add, Triad, raw units, and upstream numerical validation. Do not alter timed arithmetic kernels or introduce application memory binding that substitutes for Kubernetes placement.
- [x] Restrict thread affinity to allocated CPUs; expose process/array identity and a practical observation window after initialization and outside timed kernels.
- [x] Document harness changes and their effect on STREAM comparability. If residency instrumentation is unavailable, report the missing evidence instead of asserting array placement.
- [x] Produce machine-readable run metadata and retain raw standard output/error and failure status, including invalid numerical results and OOM.
- [x] Demonstrate the build/run/result path where the execution environment permits and test parsing/validation with explicit fixtures; identify unavailable execution rather than fabricating measurements.
- [x] Keep this ticket independent of policy/API changes, paired placement manifests, and access to HBM hardware.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 08 of 11. This is a standalone workload deliverable, so it can proceed alongside Kubernetes prefactoring. Ticket 10 combines it with the feature and runtime evidence kit.

## Answer

Resolved on 2026-10-14. [Standalone workload and operating instructions](../stream/README.md)
provide pinned official STREAM 5.10/GCC image inputs, configurable sizing and
OpenMP threads, unchanged timed kernels and upstream validation, initialized
array identity and an untimed observation window, sampled Linux residency,
and machine-readable build/run/failure evidence with retained raw output.
The external launcher retains image identity and runtime OOM status when the
inner runner is killed. Missing instrumentation is reported explicitly.

All 14 standalone CLI tests, Python typechecks, native C warning checks,
boilerplate checks, and a real serial macOS smoke run pass. Independent
Standards and Spec reviews report zero remaining findings after corrections.
See [verification-08.md](../verification-08.md) for commands, commits, red/green
observations, and limitations. Linux/OpenMP image execution, actual NUMA
residency, live container OOM, Kubernetes placement, and HBM evaluation are
NOT RUN because the current environment has no running Linux container engine.
No throughput or physical placement result is inferred from the smoke run.
