# Standalone STREAM workload

Ticket 08 packages a **variant of official STREAM 5.10**. It needs no forked API,
Kubernetes policy, HBM hardware, or paired placement manifests. Ticket 10 owns
paired runs and summaries; ticket 11 owns operator-collected runtime evidence.

## Source, build, and license

`upstream/stream.c` is an unmodified copy from the
[official source directory](https://www.cs.virginia.edu/stream/FTP/Code/stream.c).
`source.json` pins revision 5.10 and SHA-256
`a52bae5e175bea3f7832112af9c085adab47117f7d2ce219165379849231692b`.
The build rejects a checksum mismatch. Its source header contains John D.
McCalpin's license, including publication restrictions. Preserve that header
when redistributing. Follow the [official reference and run rules](https://www.cs.virginia.edu/stream/ref.html),
including attribution and the labelling requirements for modified versions.
The new harness files use Apache-2.0; this does not relicense STREAM.

The Dockerfile pins the official GCC 14.2.0 Bookworm multi-platform image by
registry digest in `base-image.txt`. It installs no packages or floating
network dependencies. Build context is this directory, not the Kubernetes tree:

```sh
cd .scratch/independent-numa-placement/stream
podman build -t numa-stream:ticket08 .
podman image inspect numa-stream:ticket08 > /tmp/numa-stream-image.json
```

Docker can replace Podman. A Linux container engine must already be running.
The multi-platform digest fixes platform-specific images; record the selected
platform and resulting image ID/digest for comparisons. Tags alone are not
build identity. The launcher records image inspection and runs by resolved ID.
Operators running directly in Kubernetes supply the workload image digest using
`--image-identity` and retain the runtime's resolved image ID separately.

The runner compiles inside each execution before allocating the arrays. The
fixed default flags are `-O3 -std=c99 -D_GNU_SOURCE -fopenmp`, plus
`-DSTREAM_ARRAY_SIZE=N -DNTIMES=I`. It records the complete command, actual
compiler version, upstream hash, instrumented-source hash, harness hashes, and
executable hash in `run.json`. `--cflag=...` appends an explicitly recorded flag.
For very large static arrays on x86-64, `--cflag=-mcmodel=medium` may be needed.
Preprocessor definition/undefinition flags and flags disabling OpenMP are rejected.
Use the workload options for array size and iterations; array type and kernels
remain fixed.
Runtime compilation consumes memory and CPU, so allow overhead and stabilize
host load before comparing independent executions.

## Run a smoke test

Run the host launcher from this directory. Choose a new output directory each
time; evidence is never overwritten. The default one-thread, ten-million-double
run is labelled **smoke**, regardless of its apparent bandwidth:

```sh
python3 container.py --engine podman --image numa-stream:ticket08 \
  --output /tmp/stream-smoke-01 --memory 1g -- \
  --array-size 1000000 --threads 1 --observe-seconds 30
```

The launcher's output directory contains `launch.json`, `image-inspect.json`,
`container-inspect.json`, `container.id`, and raw runtime stdout/stderr. Workload
artifacts are under `run/`. Stopped containers are retained for inspection;
remove the specific ID recorded in `container.id` when finished.

To run directly in a container or Pod, the image entrypoint accepts runner flags:

```sh
python3 /opt/stream/run.py --output /evidence/run \
  --array-size 1000000 --threads 1 --observe-seconds 30 \
  --image-identity 'registry.example/numa-stream@sha256:ACTUAL_DIGEST'
```

Mount a writable persistent evidence volume at `/evidence`. Use equal CPU and
memory requests/limits when this workload is later combined with the research
fork's Guaranteed-QoS placement feature. No placement fields or paired manifests
are provided by this ticket. For Kubernetes OOM evidence retain the Pod status,
including `lastState.terminated.reason`, exit code, and resolved container image;
an OOM may kill the Python runner before its final metadata is written.

On a host without Linux/OpenMP, the following explicitly serial **portability
smoke test** exercises build, run, parsing, validation, and the observation
window. It provides no Linux affinity or NUMA residency evidence:

```sh
python3 run.py --output /tmp/stream-local-01 --serial --cc cc \
  --array-size 1000000 --threads 1 --observe-seconds 0
```

## Size a bandwidth experiment

Select `--mode bandwidth --llc-bytes B` only after determining the sum of
last-level caches used by the actual allocated CPUs. Each array must contain at
least one million doubles and occupy at least four times that cache sum. The
runner enforces those inputs and requires upstream calibration of at least
20 clock ticks per test. This is a **bandwidth candidate**, not certification
of a compliant published STREAM result.

For example, a run using 32 MiB of LLC needs at least 16,777,216 doubles per
array. Three double arrays then consume 402,653,184 bytes (384 MiB). Add room
for the compiler, Python, OpenMP thread stacks, executable, observation buffers,
and other process overhead. The default image run has a 1 GiB memory limit;
set `--memory` on the host launcher to suit the actual run. In Kubernetes size
both requests and limits, and fit the arrays on the selected memory node, not
just in aggregate host memory. This harness uses ordinary double arrays; it
does not request explicit hugepages. Host transparent-hugepage settings can
still affect observations and should be recorded by an experiment operator.

A four-thread example, after substituting actual available CPU IDs:

```sh
python3 container.py --engine podman --image numa-stream:ticket08 \
  --output /tmp/stream-bandwidth-01 --memory 1g --cpuset-cpus 0-3 -- \
  --mode bandwidth --llc-bytes 33554432 --array-size 16777216 \
  --threads 4 --iterations 10 --observe-seconds 30
```

OpenMP threads use explicit places built from `sched_getaffinity(0)`,
`OMP_PROC_BIND=close`, and `OMP_DYNAMIC=FALSE`. Inherited OMP/GOMP/KMP affinity
settings are cleared. Requests exceeding the allowed CPU count are rejected.
The hook records each worker's current CPU and allowed CPUs; the runner checks
that every recorded mask is confined to the inherited allocation. It never
expands CPU affinity or calls memory binding/migration APIs. CPU quotas do not
identify dedicated CPUs; use the actual cpuset allocation. Record concurrent
load and use matched actual CPU sets for any later paired experiment.

## Observe initialized arrays

After upstream initializes all three arrays, the hook writes `arrays.json`
with the live process PID, exclusive start/end virtual addresses, byte lengths,
and phase `initialized_before_calibration`. It writes `threads.json` and, on
Linux, raw `proc-status.txt`, `proc-maps.txt`, and `proc-numa_maps.txt`. It then
waits `--observe-seconds` (default 30). The PID is in the workload's PID namespace;
a host observer must identify the matching host PID through the runtime.

While the observation window is open, an operator can read these files through
the mounted volume or container exec and correlate the address ranges with
`/proc/PID/maps`. The hook queries residency at the end of the window, **before
calibration and timed kernels**, using Linux `move_pages` with a NULL node list
and flags zero. This queries locations only; it neither binds nor migrates
pages. `residency.json` records the page size, each array's total page count,
evenly spaced sample count, syscall errno, and per-sample status (nonnegative
NUMA node ID; negative per-page error). First and last pages are included when
sampling more than one page. Boundary pages can contain parts of two arrays.

`--residency-samples` defaults to at most 4096 pages per array. A successful
`sampled` status establishes only sampled page locations at that instant, not
all-page placement or continued residency during the timed execution. If the
sample count covers all pages, the record says so numerically. Use an external
page-query collector during the window if another observation method is needed.

Seccomp, permissions, kernel support, or `/proc` restrictions can prevent
instrumentation. Failed queries retain their errno/page errors and report
`unavailable`; non-Linux runs record an explicit reason. An allowed memory mask
or whole-process `numa_maps` aggregate is not evidence of these arrays' physical
residency. Never turn missing evidence into a placement claim. Numerically
valid output with unavailable residency remains usable as a smoke result;
it cannot establish a successful controlled placement comparison.

## Comparability and results

The runner generates `stream-observed.c` by adding one header and one call to
`stream_observe(a, b, c, sizeof(a))` before upstream clock calibration. The pinned
source itself, Copy/Scale/Add/Triad arithmetic loops, timers, first-iteration
exclusion, and numerical validation remain unchanged. The hook adds an OpenMP
observation region, metadata I/O, a pause, and page queries outside timed code.
These can change cache warmth, page state, scheduling, and thermal state.
Label published results **based on a variant of the STREAM benchmark code**;
do not claim unmodified standard STREAM comparability. Keep hook settings
identical between compared runs.

`stdout.txt` and `stderr.txt` are raw workload output; `build.stdout.txt` and
`build.stderr.txt` retain compiler output. The parser preserves all four rates
in upstream **decimal MB/s**, along with average/minimum/maximum seconds.
No unit conversion is applied. If converting later, decimal GB/s = MB/s / 1000;
that is not GiB/s. Reparse an archived run with:

```sh
python3 parse.py /tmp/stream-smoke-01/run/stdout.txt --returncode 0
```

Supply the actual saved return code, including a negative signal code from the
native runner. Upstream can exit zero after printing `Failed Validation`; this
parser still returns failure. Truncated tables, wrong units, duplicate rows,
nonfinite rates, missing validation, nonzero exits, and malformed timing values
cannot produce a valid result. The runner records timeouts/signals and a
possible SIGKILL/OOM separately. Cgroup-v2 OOM counter changes are recorded when
readable; the host launcher retains the runtime's authoritative `OOMKilled`
state even when the inner runner disappears. A SIGKILL without OOM evidence is
not labelled a confirmed OOM. A stale `status: running` after interruption is
incomplete evidence, never success. All CLI success exits are zero; failures
are nonzero. A valid numerical result does not prove placement.

## Verification

The public CLI test seams were approved by the user for ticket 08. Fixtures in
`tests/fixtures/` are deliberately synthetic; their rates are not measurements.
They cover result parsing and numerical/exit failures. Runner checks compile
and execute the real source in serial smoke mode and observe live arrays before
the result table. External compiler/runtime fixtures exercise failure recording,
including OOM without an inner result.

```sh
python3 -m unittest discover -s tests -v
cc -std=c99 -D_GNU_SOURCE -Wall -Wextra -Werror -c observe.c -o /tmp/stream-observe.o
mypy run.py parse.py container.py
```

These local checks do not replace Linux/OpenMP/container execution. See
[verification-08.md](../verification-08.md) for the actual environment and
executed/unexecuted checks. No HBM performance or Kubernetes placement result
is inferred from the local smoke test.
