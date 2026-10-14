# 08: Package a reproducible STREAM workload with residency evidence

**What to build:** An operator can build and run a configurable STREAM container, obtain validated bandwidth results, and identify the live initialized arrays for memory-residency observation. This workload is usable before the new Kubernetes feature is implemented.

**Blocked by:** None (can start immediately).

**Status:** claimed

**Type:** task

- [ ] Pin official STREAM source by revision/checksum and record compiler, optimization/OpenMP flags, source license requirements, and image/build identity.
- [ ] Support configurable array size and thread count, document resource sizing and cache-sizing requirements, and distinguish small smoke tests from memory-bandwidth experiments.
- [ ] Preserve Copy, Scale, Add, Triad, raw units, and upstream numerical validation. Do not alter timed arithmetic kernels or introduce application memory binding that substitutes for Kubernetes placement.
- [ ] Restrict thread affinity to allocated CPUs; expose process/array identity and a practical observation window after initialization and outside timed kernels.
- [ ] Document harness changes and their effect on STREAM comparability. If residency instrumentation is unavailable, report the missing evidence instead of asserting array placement.
- [ ] Produce machine-readable run metadata and retain raw standard output/error and failure status, including invalid numerical results and OOM.
- [ ] Demonstrate the build/run/result path where the execution environment permits and test parsing/validation with explicit fixtures; identify unavailable execution rather than fabricating measurements.
- [ ] Keep this ticket independent of policy/API changes, paired placement manifests, and access to HBM hardware.

## Comments

Approved ticket breakdown on 2026-10-14: ticket 08 of 11. This is a standalone workload deliverable, so it can proceed alongside Kubernetes prefactoring. Ticket 10 combines it with the feature and runtime evidence kit.
