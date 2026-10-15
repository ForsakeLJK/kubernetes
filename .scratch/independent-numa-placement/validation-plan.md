# Validation and STREAM evaluation

Status: planned; no new tests or measurements executed.

Contract: [spec.md](spec.md). Delivery slices: [implementation-plan.md](implementation-plan.md).

## Automated acceptance

Use synthetic fixtures with CPU-bearing DDR node 0, CPU-less memory node 3,
another CPU-bearing node, sparse IDs, controllable capacity, and distance data.
The labels DDR/HBM in a fixture carry no simulated performance claim.

| Area | Required cases |
| --- | --- |
| API | Each new field and both; nil versus zero; round trips; mixed legacy rejection; negative/64 rejection; equal CPU/memory IDs accepted. |
| Eligibility | Guaranteed whole-CPU requirements even for memory-only requests; app/init/sidecar declarations; no Pod-level budgets. |
| Policy/config | New policy registration; pod scope only; Linux support; new fields rejected under old policies; legacy works under both; ordinary behavior delegates; global defaults unchanged. |
| Discovery | Memory-only target accepted for memory; same node rejected for CPUs; missing/sparse node IDs; topology validation independent of CPU list. |
| Planning | Both explicit; CPU-only; memory-only; same-node preference; nearest feasible rather than nearest exhausted node; directed asymmetric distances; equal-distance ID tie. |
| Distance failure | Multiple feasible nonlocal candidates with missing/malformed/ambiguous distances cause refusal; explicit pair, feasible same node, and sole feasible candidate avoid unnecessary ranking. |
| Capacity | CPU, ordinary memory, each hugepage size shortages with spare resources elsewhere; automatic side refuses when only aggregate multi-node capacity suffices. |
| Devices | CPU-compatible device hints with remote memory; incompatible hints refuse; device preferences not overwritten; preserved existing allocation behavior. |
| Enforcement | Concrete CPUs subset of selected CPU node; memory blocks/mask exactly selected memory node; fixed and automatic targets enforced equally; separate runtime masks. |
| Accounting | App/init/sidecar lifetimes, sequential reuse, multiple hugepage sizes, and no reservation leaks on successful cleanup. |
| Failure transaction | Fail each provider/container boundary; restore only newly changed accounting and affinities; preserve restored/unrelated CPU/memory/device allocations. |
| Recovery | Valid explicit/automatic targets retained; conflicting or multi-node assignments rejected; partial recovery; restart cannot reselect nearer nodes. |
| Updates | Immutable fields including add/remove; resize and ephemeral addition rejection; unrelated valid updates preserved. |
| Compatibility/metrics | Legacy/no-field regression; genuine common alignment counted; split success not counted as common alignment; admission totals accurate. |

Prefer behavioral and fault-injection tests over tests that merely duplicate
implementation details. Record test commands and their environment. Linux-only
execution remains a separate result from Linux cross-compilation.

## Manual runtime matrix

Run on a disposable Linux test environment exposing suitable NUMA topology and
the fork's matching API server/kubelet. Record actual CPU lists, online NUMA IDs,
distance rows, memory capacities, hugepages, kernel, runtime, fork commit, kubelet
configuration/reservations, container image digests, and exact manifests.

For each new-field Pod, require both managers and the `independent-numa` policy
with pod scope. Legacy tests cover the new policy and a separately configured
single-numa-node environment. Do not change policy on a live test node and erase
checkpoints merely to make recovery tests pass; use separate clean configurations.

1. Run explicit C/M pairs with different nodes, including a memory-only M where
   the guest/host supports it; also run an equal-node pair where feasible.
2. Run each partial request; verify selected singleton and locality ranking
   against the recorded feasible topology, rather than assuming node 0.
3. Inspect actual container CPU and memory-node masks while application, init,
   and sidecar processes are running. Inspect init containers before they exit.
4. Touch anonymous memory and observe its residency. Check hugepage allocations
   separately for every size provisioned. Missing capabilities mean NOT RUN.
5. Repeat after container restart and kubelet restart while preserving state;
   trigger a post-recovery container restart as well.
6. Exhaust CPU, ordinary memory, and hugepages at the selected target with spare
   capacity elsewhere; require specific refusal and no fallback or leaks.
7. Exercise invalid IDs/configurations, immutable changes, resize, ephemeral
   containers, ordinary Pods, and legacy Pods. Record the actual rejecting layer.
8. Test conflicting device topology if a suitable plugin/device is available;
   otherwise retain automated evidence and mark that runtime case NOT RUN.
9. Compare resource state before/after normal cleanup and failed admissions;
   preserve unrelated Pods and their assignments.

Result template fields: date, operator, commit, environment, case, manifest,
commands, stdout/stderr, observed masks/residency, PASS/FAIL/NOT RUN, limitations.
The user's previous manual VM testing is acknowledged; no new results are
inferred from it and the earlier dated agent execution record remains unchanged.

## STREAM experiment

Use official STREAM and follow its [reference and run rules](https://www.cs.virginia.edu/stream/ref.html).
Record Copy, Scale, Add, and Triad bandwidth and numerical validation output.
Pin the source revision/checksum, compiler, optimization/OpenMP flags, and image
digest. Preserve raw output and units; report any MB/s to GB/s conversion explicitly.

On real DDR/HBM hardware, configure matched experiments:

| Case | CPU target | Memory target | Purpose |
| --- | --- | --- | --- |
| DDR | Fixed CPU node C | Verified DDR node D | Reference bandwidth |
| HBM | Same CPU node C | Verified HBM node H | Bandwidth from selected HBM |
| Optional legacy comparison | Node C | Node C through `numaNode` | Check same-placement compatibility when C has memory |

Use both new fields for paired measurements, so automatic selection does not
change the tested topology. Partial-field correctness is covered separately.
The fixture is not required to use node IDs 0 and 3; take IDs from the host.

Experimental controls:

- Compare equal actual CPU sets, thread counts, binary, compiler flags, array
  size, memory limit, and run settings. A fixed CPU NUMA ID alone does not fix
  the actual CPU subset. Capture it; if it changes, repeat or report the mismatch.
- Use thread affinity restricted to the container's allocated CPUs, and record
  actual placement. Do not use application memory-binding commands to bypass
  the Kubernetes feature being evaluated.
- Use a configurable thread sweep (at least a one-thread baseline and available
  multi-thread counts); compare DDR/HBM at each equal count. One CPU may not
  expose available memory bandwidth. Do not oversubscribe allocated CPUs.
- Size arrays according to STREAM's rule: each array at least four times the
  total last-level cache used by the run, or one million elements, whichever is
  larger. Standard double-precision arrays consume approximately `3 * N * 8`
  bytes plus process overhead. Fit within both memory targets and the Pod limit.
  A topology-only smoke test with smaller arrays must be labeled as such.
- Initialize/touch arrays before residency observation. Capture the mappings
  backing the arrays using per-process NUMA residency information or sampled
  page-location queries as supported by the environment. Do not equate a
  whole-process aggregate or `Mems_allowed_list` with proof of array residency.
- Keep evidence collection outside timed kernels. Document any harness hooks
  used to identify arrays or pause for inspection and their impact on standard
  STREAM comparability. Do not modify arithmetic kernels to improve results.
- Run a warm-up, then at least five independent measured executions per matched
  configuration. Retain STREAM's per-execution output and report the median and
  range across those executions. Interleave DDR/HBM runs where practical and
  record concurrent workload/interference and relevant host settings.

Correctness failures, numerical validation failures, OOM, wrong placement, and
missing residency evidence prevent claiming a successful controlled comparison.
Do not replace a failed run with a speedup estimate. Throughput may favor either
memory type; there is no required HBM speedup threshold.

## VM versus physical evidence

A VM exposing virtual NUMA nodes can validate API, admission, allocation,
effective masks, and guest-visible page placement, including memory-only nodes
if the virtualization setup supports them. STREAM can verify that the workload
and reporting pipeline run and measure that environment's throughput.

Two virtual NUMA nodes backed by the same host memory do not constitute a DDR/HBM
comparison. Claim real memory-type bandwidth differences only after verifying
the backing hardware and mapping. If the user elects only VM validation, deliver
the VM findings and state that real HBM evaluation has not been performed.

Milestone 1 is code plus runnable tooling and automated evidence. Milestone 2 is
the manual runtime evidence actually collected, with unsupported/unavailable
cases explicitly identified. Keep these statuses separate in the final report.
