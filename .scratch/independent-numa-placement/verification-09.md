# Ticket 09 verification

Date: 2026-10-14. Baseline: `d72c44189e7516bf832e2c2ff677d9c5b9668044`.
Branch: `numa_support_hbm`. Environment: macOS arm64, Python 3.12.6,
Go 1.27.1, Apple Clang. This ticket changes runtime tooling and documentation;
no Kubernetes Go code, generated artifacts, modules or deployment changed.

## Delivered scope

[Operator procedure](runtime/README.md), machine-specific JSON/YAML fixture
generation for all placement forms, gate-controlled init/sidecar/application
workload, private anonymous and multi-size hugetlb allocation, query-only page
sampling, separate CPU/memory mask checking, raw command capture, and a full
case/environment evidence template. The report retains all matrix rows,
artifact hashes and explicit PASS/FAIL/NOT RUN, and refuses incomplete passes.
Refusal/ranking/lifecycle/accounting verdicts require operator adjudication;
the report checks evidence completeness rather than inferring those semantics.

The legacy checker's `--node` interface and fixtures remain available. Its new
targets support a CPU-less memory node without requiring a CPU list from it.
Strict CPU/node parsing is shared by live and archived checks. The historical
legacy results file is unchanged, and the user's earlier manual laptop-VM test
is acknowledged without inventing commands or outputs.

## Test seams and red/green record

Ticket09 explicitly requests checker/report output handling tests. New tests
use these public CLI boundaries, saved files and exit status; report setup also
exercises fixture generation and real command capture at the external boundary.
An optional request to expand agreement to all three CLIs was sent; no expanded
agreement is inferred from silence. The required checker/report seams come
from the ticket itself. Synthetic inputs are labelled and never represent
live NUMA evidence.

Observed failing checks before corrections:

- Separate CPU/memory CLI targets and archived status were unavailable.
- Touched-page checker and report CLI did not exist.
- A claimed PASS with mismatching raw/parsed masks was accepted by the report.
- A regular-file mapping labelled as a hugepage was accepted by the checker.

The corrected checks pass. Coverage also includes sparse/range masks, legacy
usage, missing/malformed masks, wrong page nodes, unavailable/partial/negative
page queries, missing hugepage sizes, malformed observations, complete and
incomplete reports, failed checkers, missing container roles, hidden matrix
rows, unfinished commands and paths outside the run directory.

## Executed commands

From the Kubernetes repository root:

- `python3 -m unittest discover -s .scratch/independent-numa-placement/runtime/tests -v`
  — **9 tests pass** (1.223 seconds).
- `python3 -m unittest discover -s .scratch/independent-numa-placement/stream/tests -v`
  — **14 tests pass** (5.284 seconds), including actual serial STREAM smoke
  execution. These are the full relevant tooling suites. Kubernetes Go suites
  were not rerun because no Go implementation/generator input changed.
- Python typechecking, repeated during implementation:
  `UV_TOOL_DIR=/private/tmp/numa-ticket-09-tools UV_TOOL_BIN_DIR=/private/tmp/numa-ticket-09-bin uv tool run --offline --cache-dir /private/tmp/numa-ticket-08-uv --from mypy==1.15.0 mypy --cache-dir /private/tmp/numa-ticket-09-mypy`
  followed by runtime `fixtures.py`, `capture.py`, `residency.py`,
  `check-residency.py`, `report.py` and legacy runtime `check-masks.py`,
  `masklib.py` — **pass**, seven source files.
- `cc -std=c99 -D_GNU_SOURCE -Wall -Wextra -Werror .scratch/independent-numa-placement/runtime/probe.c -o /private/tmp/numa-ticket09-probe`
  — **pass**, native macOS conditional path only.
- `/private/tmp/numa-ticket09-probe --bytes 65536 --hold-seconds 0 --output /private/tmp/numa-ticket09-probe-smoke`
  — **pass**, actual native anonymous allocation/touch and JSON artifact writing.
- `python3 .scratch/independent-numa-placement/runtime/check-residency.py /private/tmp/numa-ticket09-probe-smoke/observation.json --memory-node 3 --output /private/tmp/numa-ticket09-probe-smoke/check.json`
  — expected exit 2, **NOT RUN** residency: macOS returns recorded ENOSYS (78).
  No page-node observation or NUMA-placement result is claimed.
- `hack/verify-boilerplate.sh .scratch/independent-numa-placement/runtime .scratch/explicit-numa-placement/runtime/check-masks.py .scratch/explicit-numa-placement/runtime/masklib.py`
  — **pass**.
- `git diff --check` — **pass** before review.

## Runtime limitations

Linux image build/run, Linux syscall branch, hugetlbfs mount/allocation,
actual NUMA page queries, matched fork deployment, effective live container
masks, VM/hardware matrix and DDR/HBM evaluation are **NOT RUN**. Ticket11 owns
live execution. Native compilation and synthetic checker success do not replace
these observations. Ticket10 owns paired STREAM automation.

## Review

Standards and Spec reviews against the starting commit are pending.
