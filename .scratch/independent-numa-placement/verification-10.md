# Ticket 10 verification

Date: 2026-10-15. Baseline: `dc865c95f82bafec73fe9b9e7b5f1e02a1fcb9ad`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Python 3.12.6,
Go 1.27.1, native Apple C compiler. Only tooling/documentation changed.

## Delivered scope

[Paired CLI and procedure](evaluation/README.md), explicit CPU/memory manifests,
equal Guaranteed resources, one-thread/multi-thread sweep, cache/capacity sizing,
alternating warm-up and >=5 measured pairs, shared raw command capture,
actual CPU sets/worker mapping, source/binary/image matching, numeric/raw-output
checks, initialized-array residency checks, retained per-execution evidence and
decimal-MB/s median/range reports. VM, neutral physical, operator-verified
DDR/HBM, smoke and synthetic classifications remain distinct. See
[milestone-1.md](milestone-1.md) for accumulated readiness and runtime limits.

## Test seams and red/green evidence

Ticket 10 explicitly requires orchestration/report checks on synthetic or
recorded fixtures. Tests use the public prepare/run/report CLI, saved outputs
and exit codes; fake kubectl replaces only the external process boundary.
No private helpers are tested. All fixture rates/page records are synthetic.

The first manifest/schedule CLI test failed because `paired.py` was absent,
then passed after preparation was implemented. The first full-fixture report
test failed because the evidence reporter was absent, then passed after raw
evidence assessment and summarization were implemented. Subsequent slices
exercise median/range and warm-up exclusion, invalid sizing/thread/run inputs,
actual CPU mismatch despite equal NUMA IDs, honest/incorrect binary hashes,
numeric failure, missing/malformed/wrong residency, incomplete schedules,
backing-type labels, API refusal and sequential cleanup/retrieval.

## Executed checks

From the Kubernetes repository root:

- `python3 -m unittest discover -s .scratch/independent-numa-placement/evaluation/tests -v`
  — pre-review: 13 tests pass, 9.298 seconds; post-correction: 15 tests pass,
  10.898 seconds; final: 16 tests pass, 11.227 seconds. Includes a full
  12-execution synthetic cluster
  smoke through shared command capture, tar retrieval, report generation and
  deletion of every created Pod; this is not live Kubernetes execution.
- `python3 -m unittest discover -s .scratch/independent-numa-placement/stream/tests -v`
  — 14 tests pass, 5.116 seconds, including real native serial STREAM smoke.
- `python3 -m unittest discover -s .scratch/independent-numa-placement/runtime/tests -v`
  — 12 tests pass, 1.712 seconds.
- Repeated typechecking: `UV_TOOL_DIR=/private/tmp/numa-ticket-10-tools UV_TOOL_BIN_DIR=/private/tmp/numa-ticket-10-bin MYPYPATH=.scratch/independent-numa-placement/stream:.scratch/explicit-numa-placement/runtime uv tool run --offline --cache-dir /private/tmp/numa-ticket-08-uv --from mypy==1.15.0 mypy --cache-dir /private/tmp/numa-ticket-10-mypy .scratch/independent-numa-placement/evaluation/paired.py .scratch/independent-numa-placement/evaluation/evidence.py`
  — passes, both new source files.
- `hack/verify-boilerplate.sh .scratch/independent-numa-placement/evaluation`
  — passes.
- `git diff --check` — passes.
- `podman machine list --format json` — `[]`; no configured Linux VM.

These are the full relevant tooling suites. No Go implementation, generator
input or dependency changed; Kubernetes-wide suites/generators were not rerun.
Earlier repository-wide failures remain documented in tickets 01–07.

## Unavailable runtime evidence

Linux image build/run, actual OpenMP affinity, real kubelet configz/deployment,
NUMA masks, initialized-array page queries on Linux, VM/hardware STREAM runs,
and verified DDR/HBM throughput are NOT RUN. No external host is required to
deliver ticket 10. Ticket 11 retains all user-operated runtime work. Synthetic
fixture throughput is never presented as a measurement or speedup estimate.

## Review

Implementation commits: `d1ee8ab825e`, `6d141ff5d8e`, `f39bebad16b`. Independent Standards
and Spec reviews use the fixed baseline above.

Initial Standards review found a P2 cleanup bug after an API-accepted create
with a lost client response. Initial Spec review found a P2 false resource
mismatch after Kubernetes canonicalizes `1024Mi` to `1Gi`. Both were reproduced
with failing public CLI regressions, then corrected. Cleanup now reconciles a
random ownership label after any attempted creation and preserves unrelated
name collisions. Resource comparison checks numeric CPU/memory quantities.
Both regressions and all 15 evaluation tests passed; typechecking and boilerplate
checks passed again. Follow-up Spec review found a P3 equivalent CPU
canonicalization edge (`1000` to `1k`). An additional public CLI regression
failed before correction and passes with exact DecimalSI CPU parsing; changed
quantities still fail. Unchanged STREAM/runtime suites remain green from the
full run above.

## Standards

The cleanup finding is resolved: independent re-review confirmed owned-Pod
cleanup after a failed create response and preservation of a conflicting owner.
No documented-standard violations or actionable baseline smells remained.
Final focused review through `f39bebad16b` also found no concrete defects or
documented-standard violations in exact DecimalSI CPU comparison.

## Spec

Final independent review through `f39bebad16b`: both memory/CPU quantity
findings are resolved. Equivalent canonical quantities compare equal and
changed values fail. No remaining actionable Spec findings; runtime limitations
remain consistent with ticket 10's approved software/tooling scope.

Review totals: Standards 0 remaining; Spec 0 remaining. Final relevant suites:
16 evaluation + 14 STREAM + 12 runtime = 42 tests pass. Typechecking,
boilerplate and diff checks pass after code corrections.
