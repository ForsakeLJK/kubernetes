#!/usr/bin/env python3
# Copyright The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


"""Build and run a pinned STREAM variant, retaining raw output and observation artifacts."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import time
from typing import Any

from parse import parse_output

ROOT = Path(__file__).resolve().parent
# Insert only after the upstream initialization loop, before checktick and all timers.
ANCHOR = '    if  ( (quantum = checktick()) >= 1) '


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def save(path: Path, value: Any) -> None:
    temporary = path.with_suffix(path.suffix + '.tmp')
    temporary.write_text(json.dumps(value, indent=2, allow_nan=False) + '\n')
    temporary.replace(path)


def memory_events() -> dict[str, int]:
    """Read only the process's cgroup v2 counters; no host-global OOM inference."""
    try:
        group = next(line[3:] for line in Path('/proc/self/cgroup').read_text().splitlines()
                     if line.startswith('0::'))
        root = Path('/sys/fs/cgroup').resolve()
        directory = (root / group.lstrip('/')).resolve()
        if not directory.is_relative_to(root):
            return {}
        return {key: int(value) for key, value in
                (line.split() for line in (directory / 'memory.events').read_text().splitlines())}
    except (OSError, ValueError, StopIteration):
        return {}


def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True, help='new evidence directory')
    parser.add_argument('--array-size', type=int, default=10000000, help='double elements per array')
    parser.add_argument('--threads', type=int, default=1)
    parser.add_argument('--iterations', type=int, default=10)
    parser.add_argument('--observe-seconds', type=int, default=30)
    parser.add_argument('--residency-samples', type=int, default=4096, help='maximum pages queried per array')
    parser.add_argument('--mode', choices=['smoke', 'bandwidth'], default='smoke')
    parser.add_argument('--llc-bytes', type=int, help='sum of last-level cache bytes used by this run')
    parser.add_argument('--serial', action='store_true', help='explicit non-OpenMP portability smoke run')
    parser.add_argument('--cc', default='gcc')
    parser.add_argument('--cflag', action='append', default=[], help='additional flag; use --cflag=-mcmodel=medium')
    parser.add_argument('--timeout', type=int, default=600, help='seconds including observation window')
    parser.add_argument('--image-identity', help='resolved image ID/digest from the launcher or operator')
    return parser.parse_args()


def execute(args: argparse.Namespace, meta: dict[str, Any]) -> int:
    output = args.output
    if not (1 <= args.array_size <= (2**63 - 1) // 24):
        raise ValueError('--array-size must be positive and fit three double arrays in 64-bit address space')
    if args.threads < 1 or not 2 <= args.iterations <= 100:
        raise ValueError('--threads must be positive; --iterations must be 2..100')
    if not 0 <= args.observe_seconds <= 3600 or not 1 <= args.residency_samples <= 1000000:
        raise ValueError('--observe-seconds must be 0..3600; --residency-samples must be 1..1000000')
    if args.timeout <= args.observe_seconds:
        raise ValueError('--timeout must exceed --observe-seconds')
    if any(flag.startswith(('-D', '-U', '-Wp,', '-Xpreprocessor', '-fno-openmp')) for flag in args.cflag):
        raise ValueError('--cflag cannot override preprocessor definitions or disable OpenMP; use the workload options')
    if args.serial and (args.threads != 1 or args.mode != 'smoke'):
        raise ValueError('--serial supports only one-thread smoke runs')
    cpus = sorted(os.sched_getaffinity(0)) if hasattr(os, 'sched_getaffinity') else None
    meta['allocated_cpus'] = cpus
    if not args.serial and not cpus:
        raise ValueError('OpenMP run requires Linux sched_getaffinity; use --serial for a portability smoke run')
    if cpus and args.threads > len(cpus):
        raise ValueError(f'--threads={args.threads} exceeds {len(cpus)} allocated CPUs')
    if args.mode == 'bandwidth':
        if not args.llc_bytes or args.llc_bytes <= 0:
            raise ValueError('--mode bandwidth requires --llc-bytes from the actual allocated CPUs')
        if args.array_size < 1000000 or args.array_size * 8 < 4 * args.llc_bytes:
            raise ValueError('each array must contain >=1000000 doubles and >=4 times the relevant LLC bytes')
    meta['classification'] = 'bandwidth_candidate' if args.mode == 'bandwidth' else 'smoke'
    source = json.loads((ROOT / 'source.json').read_text())
    upstream = ROOT / 'upstream/stream.c'
    if sha256(upstream) != source['sha256']:
        raise ValueError('official STREAM source checksum mismatch; restore upstream/stream.c')
    text = upstream.read_text()
    if text.count(ANCHOR) != 1:
        raise ValueError('STREAM initialization hook anchor is not unique')
    instrumented = output / 'stream-observed.c'
    instrumented.write_text('#include "observe.h"\n' + text.replace(
        ANCHOR, '    stream_observe(a, b, c, sizeof(a));\n\n' + ANCHOR))
    binary = output / 'stream'
    command = [args.cc, '-O3', '-std=c99', '-D_GNU_SOURCE',
               f'-DSTREAM_ARRAY_SIZE={args.array_size}', f'-DNTIMES={args.iterations}']
    if not args.serial:
        command.append('-fopenmp')
    command += args.cflag + ['-I', str(ROOT), str(instrumented), str(ROOT / 'observe.c'), '-o', str(binary)]
    compiler = subprocess.run([args.cc, '--version'], capture_output=True, text=True, check=True)
    meta['build'] = {'source': source, 'compiler': compiler.stdout, 'command': command,
                     'base_image': (ROOT / 'base-image.txt').read_text().strip(),
                     'image_identity': args.image_identity,
                     'harness_sha256': {name: sha256(ROOT / name) for name in
                                        ['run.py', 'parse.py', 'observe.c', 'observe.h']},
                     'instrumented_source_sha256': sha256(instrumented)}
    save(output / 'run.json', meta)
    with (output / 'build.stdout.txt').open('wb') as stdout, (output / 'build.stderr.txt').open('wb') as stderr:
        build = subprocess.run(command, stdout=stdout, stderr=stderr)
    if build.returncode:
        meta.update(status='build_failed', build_returncode=build.returncode)
        return 1
    meta['build']['binary_sha256'] = sha256(binary)
    environment = {key: value for key, value in os.environ.items()
                   if not key.startswith(('OMP_', 'GOMP_', 'KMP_'))}
    settings = {'OMP_NUM_THREADS': str(args.threads), 'OMP_DYNAMIC': 'FALSE',
                'OMP_PROC_BIND': 'close', 'OMP_PLACES': ','.join('{%d}' % cpu for cpu in (cpus or [])),
                'STREAM_RUN_DIR': str(output), 'STREAM_OBSERVE_SECONDS': str(args.observe_seconds),
                'STREAM_RESIDENCY_SAMPLES': str(args.residency_samples)}
    environment.update(settings)
    meta['environment'] = settings
    meta['oom_before'] = memory_events()
    save(output / 'run.json', meta)
    with (output / 'stdout.txt').open('wb') as stdout, (output / 'stderr.txt').open('wb') as stderr:
        child = subprocess.Popen([str(binary)], stdout=stdout, stderr=stderr, env=environment)
        meta['pid'] = child.pid
        save(output / 'run.json', meta)
        previous = {}
        for sig in (signal.SIGINT, signal.SIGTERM):
            previous[sig] = signal.signal(sig, lambda signum, frame: child.send_signal(signum))
        try:
            try:
                code = child.wait(timeout=args.timeout)
            except subprocess.TimeoutExpired:
                child.kill()
                code = child.wait()
                meta['timed_out'] = True
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)
    meta['oom_after'] = memory_events()
    confirmed_oom = ('oom_kill' in meta['oom_before'] and 'oom_kill' in meta['oom_after']
                     and meta['oom_after']['oom_kill'] > meta['oom_before']['oom_kill'])
    meta['oom'] = 'confirmed_in_cgroup' if confirmed_oom else ('possible' if code in (-9, 137) else 'not_observed')
    result = parse_output((output / 'stdout.txt').read_text(errors='replace'), code)
    meta.update(result=result, returncode=code, status=result['status'])
    if confirmed_oom:
        meta['status'] = 'oom'
    elif meta.get('timed_out'):
        meta['status'] = 'timeout'
    meta['residency'] = json.loads((output / 'residency.json').read_text()) if (output / 'residency.json').exists() else {
        'status': 'unavailable', 'reason': 'process did not finish the observation hook'}
    if meta['status'] == 'valid':
        identity = json.loads((output / 'arrays.json').read_text())
        arrays = identity['arrays']
        valid_identity = (identity['pid'] == child.pid
                          and identity['phase'] == 'initialized_before_calibration'
                          and [array['name'] for array in arrays] == ['a', 'b', 'c']
                          and all(array['bytes'] == args.array_size * 8
                                  and int(array['end'], 16) - int(array['start'], 16) == array['bytes']
                                  for array in arrays))
        meta['array_identity_status'] = 'verified' if valid_identity else 'invalid'
        observed = json.loads((output / 'threads.json').read_text())['threads']
        meta['observed_threads'] = len(observed)
        if not valid_identity or len(observed) != args.threads:
            meta['status'] = 'observation_invalid'
        meta['affinity_status'] = 'unavailable'
        if cpus:
            confined = all(thread.get('allowed_cpus')
                           and set(thread['allowed_cpus']) <= set(cpus)
                           and thread.get('cpu') in thread['allowed_cpus'] for thread in observed)
            meta['affinity_status'] = 'verified' if confined else 'invalid'
            if not confined:
                meta['status'] = 'affinity_invalid'
        raw = (output / 'stdout.txt').read_text(errors='replace')
        actual_size = re.search(r'Array size = (\d+) \(elements\)', raw)
        actual_type = re.search(r'This system uses (\d+) bytes per array element', raw)
        if not actual_size or int(actual_size[1]) != args.array_size or not actual_type or actual_type[1] != '8':
            meta['status'] = 'configuration_mismatch'
        actual_iterations = re.search(r'Each kernel will be executed (\d+) times', raw)
        if not actual_iterations or int(actual_iterations[1]) != args.iterations:
            meta['status'] = 'configuration_mismatch'
        if args.mode == 'bandwidth' and (result['calibration_ticks'] or 0) < 20:
            meta['status'] = 'invalid_timing'
    return 0 if meta['status'] == 'valid' else 1


def main() -> int:
    args = arguments()
    args.output = args.output.resolve()
    # A previous run must never be overwritten or mistaken for this run's evidence.
    args.output.mkdir(parents=True, exist_ok=False)
    config = vars(args).copy()
    config['output'] = str(args.output)
    meta: dict[str, Any] = {'schema_version': 1, 'status': 'running', 'config': config,
                            'started_unix': time.time(), 'platform': platform.platform()}
    for name in ['stdout.txt', 'stderr.txt', 'build.stdout.txt', 'build.stderr.txt']:
        (args.output / name).touch()
    save(args.output / 'run.json', meta)
    try:
        code = execute(args, meta)
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        meta.update(status='setup_failed', error=str(error))
        code = 1
    meta['finished_unix'] = time.time()
    save(args.output / 'run.json', meta)
    print(json.dumps({'status': meta['status'], 'output': str(args.output)}))
    return code


if __name__ == '__main__':
    raise SystemExit(main())
