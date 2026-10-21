# STREAM VM evidence reviewed 2026-10-21

The operator ran STREAM in QEMU Ubuntu; the assistant inspected the copied raw
archive locally. No guest execution by the assistant is claimed.

- [Report](stream-report.json): `PASS_SMOKE`, `vm_measurement`, 36/36 executions PASS.
- [Audit](audit.json): all 2,424 listed artifact SHA-256 values match; local
  `paired.checked_plan` and `evidence.summarize` reproduce the original report exactly.
- [Full local archive](../../../../../../stream-smoke.nnftze4f/): retained in the workspace outside the Kubernetes
  checkout. The report and audit here do not replace that raw archive; preserve
  it when moving this evidence to another machine.

The original guest archive is
`/home/jukeliu/numa-ticket11.ogQweit2/stream-smoke.nnftze4f`.
The deployed fork is `b76f73a6f478ef3ed63cdf58bcb295dfcec1013b`;
this does not validate later source corrections or the current dirty checkout.

## Verified scope

All runs allocated CPUs 1–3 on guest node 0, with workers on CPU 1, CPUs 1–2,
or CPUs 1–3 for thread counts 1, 2, or 3. Cases A/B requested memory node 0/1.
Each Pod had a 512Mi memory limit and three arrays of 1,000,000 doubles.
There were six warm-ups and 30 measured executions. Numerical validation,
image/build consistency, effective masks, worker pinning and page-location
checks passed. All 36 archive retrieval and Pod deletion commands succeeded.
Deletion success is not an independent manager-checkpoint reclamation audit.

The 108 array records each identify 1,954 base pages of 4,096 bytes, with all
1,954 queried and all returned IDs matching the target guest node. This is
full base-page coverage at the pre-timing observation point. It does not prove
continuous residency during the timed loops or independent huge-page coverage.

Guest cache sizes are unavailable, so `llc_bytes` remains null. Rates are
smoke diagnostics only; `successful_controlled_comparison` is false by design.
Host memory types/mapping are unverified; no DDR/HBM claim is supported.

## Tooling and environment corrections

The fresh cluster followed the user's explicit decision to abandon recovery
of the older run while retaining its evidence. Hostname resolution was changed
from loopback to the guest InternalIP to allow kubelet configz access. The GCC
base reference was qualified as `docker.io/library/gcc` without changing its
digest. Setup and evaluation now retain missing cache metadata and permit
explicitly unknown LLC only for smoke configuration; bandwidth remains strict.
The evaluation regression suite passed all 20 tests before this run.
