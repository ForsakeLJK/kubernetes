# Ticket 08 verification

Date: 2026-10-14. Baseline: `7deedd3b571e8b1a93154940bc6f271676e939e0`.
Branch: `numa_support_hbm`. Environment: macOS 26.5.2 arm64, Python 3.12.6,
Apple Clang 21.0.0. Implementation commits: `e08c6ea2932`, `5f9970db2f4`.

## Delivered scope

The standalone [STREAM workload](stream/README.md) vendors official STREAM
5.10 with its original license/header and pinned SHA-256. The container recipe
pins the official GCC 14.2.0 Bookworm multi-platform image by digest and adds no
network package dependencies. Array size, threads, iterations, observation
window, sample count, cache-sizing input, and memory-limit examples are
configurable. OpenMP affinity is restricted to inherited allocated CPUs;
actual worker masks are recorded and checked. No application memory binding or
migration substitutes for Kubernetes placement.

One hook after initialization exposes the live PID and all three array address
ranges, pauses before upstream calibration, and queries sampled page locations
on supported Linux kernels. Artifacts become visible after successful close
and atomic rename. The timed arithmetic kernels, timer calls, iteration
exclusion, and upstream numerical validation remain unchanged. The README
explains instrumentation effects, resource/cache sizing, sampling limits,
missing evidence, raw decimal MB/s, and publication labelling as a STREAM
variant. The exact upstream bytes are protected by scoped Git attributes,
including their original trailing whitespace and line endings.

Run metadata records source/compiler/flags, harness and binary hashes,
configuration, platform, process identity, validation, raw units, worker/array
checks, signal/timeout status, cgroup OOM evidence when available, and missing
residency evidence. Raw compiler and workload stdout/stderr are retained. The
host launcher records the resolved image ID and runtime inspection, including
OOM when no inner result survives. Kubernetes operators are instructed to
retain Pod termination status and runtime image identity separately.

No Kubernetes API/policy, Go source, generated artifacts, modules, paired
manifests, runtime configuration, or external deployment changed. The existing
untracked planning files and later tickets were left untouched.

## Approved seams and red/green observations

The user explicitly approved the parser CLI and runner CLI boundaries, including
saved metadata, raw output, and exit status. The container launcher is tested
as the runner's external-runtime boundary. Tests use explicit synthetic result
fixtures and fixture programs only at external compiler/runtime boundaries;
fixture rates are never treated as measurements.

Recorded failing checks before implementation/correction:

- Parser CLI and runner CLI initially did not exist.
- Numerical failure with upstream exit zero was incorrectly accepted.
- Truncated output and a failed process with apparently valid output were
  incorrectly accepted.
- Nonfinite rates failed JSON serialization instead of producing invalid output.
- Observation identity checks were missing from run metadata.
- Runtime OOM with no inner result had no external metadata path.
- A reserved compiler macro could silently override the requested iteration
  count while the run remained valid.
- A malformed numerical-validation threshold (`e`) was incorrectly accepted.

The corrected checks pass. Coverage also rejects wrong units, duplicate rows,
inconsistent timing columns, missing validation, invalid size/thread/iteration
settings, and compiler failure. A real serial execution validates all three
arrays. A separate live-window check observes the initialized arrays' PID while
the runner is still alive and before the result table is emitted.

## Commands and results

Run from the Kubernetes repository root unless indicated otherwise.

- Full standalone workload suite:
  `python3 -m unittest discover -s .scratch/independent-numa-placement/stream/tests -v`
  — **14 tests pass** after review corrections (4.687 seconds).
  An earlier pre-review suite passed 12 tests; the expanded suite was rerun
  because review exposed two new issues. Final log:
  `/private/tmp/numa-ticket-08-tests-final.log`.
- Python typechecking, run repeatedly during implementation and on the final
  correction: `mypy run.py parse.py container.py` from `stream/` — pass.
  Mypy 1.15.0 was downloaded into a temporary tool cache. Actual invocation used
  `UV_TOOL_DIR=/private/tmp/numa-ticket-08-tools`,
  `UV_TOOL_BIN_DIR=/private/tmp/numa-ticket-08-bin`, and
  `uv tool run --offline --cache-dir /private/tmp/numa-ticket-08-uv --from mypy==1.15.0 mypy --cache-dir /private/tmp/numa-ticket-08-mypy`
  followed by the three repository-relative script paths.
- Native observation-hook compilation:
  `cc -std=c99 -D_GNU_SOURCE -Wall -Wextra -Werror -c .scratch/independent-numa-placement/stream/observe.c -o /private/tmp/numa-ticket-08-observe-final.o`
  — pass. This compiles the macOS/serial path; it is not Linux/OpenMP verification.
- Real smoke build/run:
  `python3 .scratch/independent-numa-placement/stream/run.py --output /private/tmp/numa-ticket-08-smoke-final --serial --cc cc --array-size 1000000 --threads 1 --observe-seconds 0`
  — exit zero, numerical validation passes; array identity and one worker are
  verified. Residency and affinity report unavailable on macOS. All four
  raw bandwidth rates are retained as smoke-only local output, without a
  main-memory, NUMA, or HBM performance claim.
- Archived-result parsing:
  `python3 .scratch/independent-numa-placement/stream/parse.py /private/tmp/numa-ticket-08-smoke-final/stdout.txt --returncode 0`
  — pass. Parsed artifact: `/private/tmp/numa-ticket-08-parsed-final.json`.
- Source hash and generated-source comparison — pass: official source hash
  `a52bae5e175bea3f7832112af9c085adab47117f7d2ce219165379849231692b`;
  generated source adds only the observation header and call before calibration.
- Changed Python files: `hack/verify-boilerplate.sh` — pass.
- `git diff 7deedd3b571e8b1a93154940bc6f271676e939e0...HEAD --check` — pass,
  with exact third-party bytes preserved through scoped attributes.

The workload suite is the complete test suite for this standalone ticket.
Kubernetes Go tests/generators were not rerun: this change touches no Kubernetes
implementation or generator input, and the approved plan discourages unrelated
repository-wide execution without a concrete need.

## Unavailable execution

Podman is installed, but `podman machine list --format json` returned `[]`,
and no runtime socket was reachable. The actual launcher attempt:

`python3 .scratch/independent-numa-placement/stream/container.py --image numa-stream:ticket08 --output /private/tmp/numa-ticket-08-no-container -- --array-size 1000000 --observe-seconds 0`

returns nonzero with `launch_failed`, preserving image-inspection stderr and
metadata. This is an environment failure, not a successful container test.
The GCC digest was independently resolved against the official Docker registry;
the Dockerfile itself was not built or executed here.

**NOT RUN:** Linux image build, Linux/OpenMP worker placement, actual
`move_pages` queries/array residency, live container OOM, Kubernetes enforcement,
VM placement, physical DDR/HBM experiments, and paired performance comparisons.
OOM/failure recording is verified with explicit external-runtime fixtures only.
A successful native C compile does not establish the Linux conditional path.
No measured performance value or placement result is fabricated.

## Standards

Initial independent Standards review found one P3 documented-standard issue:
evidence-write failures omitted the artifact path and OS error. The correction
reports both, and final review reports **0 remaining findings** and no actionable
baseline smells. Third-party source was excluded from style/smell review.

## Spec

Initial independent Spec review reproduced a P2 iteration-setting override and
a P3 malformed-validation-marker acceptance. Both were corrected with regressions;
the reviewer reran both repros and an ordinary serial smoke execution. Final
Spec review reports **0 remaining findings**, with no missing standalone
requirements or scope creep. Linux/container paths remain explicitly unverified.

Review totals: Standards 0 remaining; Spec 0 remaining. The tooling ticket is
resolved; later runtime and paired-evaluation tickets remain separate work.
