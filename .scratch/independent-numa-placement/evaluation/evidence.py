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

"""Recheck raw STREAM evidence before producing controlled paired summaries."""

import hashlib
import json
from pathlib import Path
import re
import statistics
import sys
from typing import Any

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT.parent/'stream'))
sys.path.insert(0, str(ROOT.parent.parent/'explicit-numa-placement/runtime'))
from parse import KERNELS, parse_output
from masklib import parse_list, status_lists


def load(path: Path) -> Any:
    return json.loads(path.read_text())


def require(condition: Any, message: str) -> None:
    if not condition:
        raise ValueError(message)


def file_hash(path: Path) -> str:
    with path.open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def residency_check(run: Path, size: int, node: int, samples: int, pid: int) -> None:
    identity, observation = load(run/'arrays.json'), load(run/'residency.json')
    require(identity['pid'] == pid and type(pid) is int and pid > 0
            and identity['phase'] == 'initialized_before_calibration', 'wrong process or array initialization phase')
    require(observation['status'] == 'sampled' and observation['method'] == 'move_pages query only',
            'missing sampled live array residency evidence')
    page = observation['page_size']
    require(type(page) is int and page > 0 and page & (page-1) == 0, 'invalid residency page size')
    arrays, queried = identity['arrays'], observation['arrays']
    require([a['name'] for a in arrays] == list('abc') and [a['name'] for a in queried] == list('abc'),
            'missing or duplicate array residency identities')
    ranges = []
    for array, query in zip(arrays, queried):
        start, end = int(array['start'], 16), int(array['end'], 16)
        require(start > 0 and end-start == array['bytes'] == size*8, 'array address/length mismatch')
        ranges.append((start, end))
        total = (end-1-(start-start % page))//page+1
        require(query['total_pages'] == total and query['sampled_pages'] == min(total, samples)
                and query['errno'] == 0 and len(query['page_status']) == query['sampled_pages'],
                'incomplete array page query evidence')
        require(all(type(n) is int and n == node for n in query['page_status']),
                f'array {array["name"]}: wrong page residency or per-page query errors; expected node {node}')
    ranges.sort()
    require(all(ranges[i][1] <= ranges[i+1][0] for i in range(2)), 'overlapping array address identities')


def assess(directory: Path, c: dict[str, Any], row: dict[str, Any], expected: dict[str, Any]) -> dict[str, Any]:
    result: dict[str, Any] = {'id':row['id'], 'case':row['case'], 'threads':row['threads'],
                             'phase':row['phase'], 'status':'NOT RUN', 'reasons':[]}
    try:
        orchestration = load(directory/'orchestration.json')
        require(orchestration['status'] == 'completed', f"orchestration failed: {orchestration.get('error')}")
        require(orchestration['evidence_origin'] == c['evidence_origin'], 'fixture/live evidence origin mismatch')
        if c['evidence_origin'] == 'live':
            for label in ('create', 'archive', 'pod', 'delete'):
                command = load(directory/label/'command.json')
                require(command['status'] == 'completed' and command['returncode'] == 0 and command.get('finished'),
                        f'{label}: incomplete or failed command evidence')
        wrapper = directory/'workload'
        require(load(wrapper/'execution.json')['returncode'] == 0, 'workload launcher returned nonzero')
        pod = load(directory/'pod/stdout.txt')
        require(pod['metadata']['name'] == row['pod'] and pod['metadata']['namespace'] == c['namespace'],
                'Pod evidence identity mismatch')
        for field in ('nodeName', 'cpuNUMA', 'memNUMA', 'restartPolicy'):
            require(pod['spec'][field] == expected['spec'][field], f'Pod {field} mismatch')
        container = next(p for p in pod['spec']['containers'] if p['name'] == 'stream')
        for field in ('resources', 'image', 'command'):
            require(container[field] == expected['spec']['containers'][0][field], f'Pod {field} mismatch')
        status = pod['status']
        require(status['phase'] == 'Running' and status['qosClass'] == 'Guaranteed', 'collector not Running/Guaranteed')
        state = next(s for s in status['containerStatuses'] if s['name'] == 'stream')
        require(state['restartCount'] == 0 and 'running' in state['state'] and not state.get('lastState'),
                'container exit/restart/OOM evidence prevents comparison')
        require(re.search(r'sha256:[0-9a-f]{64}', state['imageID']), 'runtime image ID is missing an immutable digest')
        run = wrapper/'run'
        meta = load(run/'run.json')
        require(meta['status'] == 'valid' and meta['returncode'] == 0
                and meta['oom'] == 'not_observed' and not meta.get('timed_out'),
                'workload failed, OOM, interrupted, or timed out')
        require(meta['array_identity_status'] == 'verified' and meta['affinity_status'] == 'verified'
                and meta['observed_threads'] == row['threads'], 'workload observation verification failed')
        parsed = parse_output((run/'stdout.txt').read_text(), meta['returncode'])
        require(parsed['status'] == 'valid', f"raw STREAM numerical/output validation: {parsed['status']}")
        raw = (run/'stdout.txt').read_text()
        for pattern, value in [(r'Array size = (\d+) \(elements\)', c['array_size']),
                                (r'This system uses (\d+) bytes per array element', 8),
                                (r'Each kernel will be executed (\d+) times', c['iterations']),
                                (r'Number of Threads requested = (\d+)', row['threads']),
                                (r'Number of Threads counted = (\d+)', row['threads'])]:
            match = re.search(pattern, raw)
            require(match and int(match[1]) == value, 'raw STREAM workload configuration mismatch')
        if c['mode'] == 'bandwidth':
            require((parsed['calibration_ticks'] or 0) >= 20, 'insufficient STREAM calibration ticks')
        settings = meta['config']
        for name in ('array_size', 'iterations', 'observe_seconds', 'residency_samples', 'timeout', 'mode', 'llc_bytes'):
            require(settings[name] == c[name], f'workload config {name} mismatch')
        require(settings['threads'] == row['threads'] and not settings['serial'] and settings['cflag'] == c['cflags'],
                'thread/serial/compiler flag configuration mismatch')
        cpus, mems = status_lists((wrapper/'effective-status.txt').read_text())
        node_cpus = parse_list((wrapper/'node-cpus.txt').read_text())
        target = c['memory_nodes'][0 if row['case'] == 'a' else 1]
        require(cpus and cpus <= node_cpus and len(cpus) == c['cpu'], 'effective CPU allocation outside target or wrong size')
        require(mems == {target}, f'effective memory mask must be exactly node {target}')
        require(meta['allocated_cpus'] == sorted(cpus), 'workload CPU allocation differs from effective mask')
        thread_cpus, thread_mems = status_lists((run/'proc-status.txt').read_text())
        require(thread_cpus and thread_cpus <= cpus and thread_mems == {target}, 'live STREAM masks outside requested targets')
        observed = load(run/'threads.json')['threads']
        require(len(observed) == row['threads'] <= len(cpus)
                and sorted(t['thread'] for t in observed) == list(range(row['threads'])), 'missing/oversubscribed workers')
        worker_cpus = []
        for thread in sorted(observed, key=lambda t: t['thread']):
            allowed = thread['allowed_cpus']
            require(len(allowed) == 1 and set(allowed) <= cpus and thread['cpu'] in allowed,
                    'worker is not pinned to one allocated CPU')
            worker_cpus.append(thread['cpu'])
        require(len(set(worker_cpus)) == len(worker_cpus), 'workers oversubscribe the same CPU')
        host = load(wrapper/'host.json')
        require(type(host['llc_bytes']) is int and 0 < host['llc_bytes'] <= c['llc_bytes'],
                'actual allocated CPU LLC exceeds the recorded array-sizing budget')
        residency_check(run, c['array_size'], target, c['residency_samples'], meta['pid'])
        env = meta['environment']
        require(env == {'OMP_NUM_THREADS':str(row['threads']), 'OMP_DYNAMIC':'FALSE', 'OMP_PROC_BIND':'close',
                        'OMP_PLACES':','.join('{%d}' % cpu for cpu in sorted(cpus)),
                        'STREAM_RUN_DIR':'/evidence/run', 'STREAM_OBSERVE_SECONDS':str(c['observe_seconds']),
                        'STREAM_RESIDENCY_SAMPLES':str(c['residency_samples'])}, 'OpenMP/observation environment mismatch')
        build = meta['build']
        require(build['source'] == load(ROOT.parent/'stream/source.json'), 'unrecognized official STREAM source identity')
        require(build['image_identity'] == c['image'], 'workload image identity mismatch')
        for name in ('binary_sha256', 'instrumented_source_sha256'):
            require(re.fullmatch('[0-9a-f]{64}', build[name]), f'missing {name}')
        require(build['binary_sha256'] == file_hash(run/'stream'), 'retained binary hash disagrees with build metadata')
        require(build['instrumented_source_sha256'] == file_hash(run/'stream-observed.c'), 'retained source hash disagrees with build metadata')
        require(build['harness_sha256'] == {name:file_hash(ROOT.parent/'stream'/name)
                    for name in ('run.py', 'parse.py', 'observe.c', 'observe.h')}, 'missing or different workload harness build hashes')
        result.update(status='PASS', rates=parsed['kernels'], cpu_set=sorted(cpus), worker_cpus=worker_cpus,
                      build=build, runtime_image=state['imageID'], llc_bytes=host['llc_bytes'],
                      sampled_residency='verified_before_timing', raw_output=str((run/'stdout.txt').relative_to(directory.parent.parent)))
    except FileNotFoundError as error:
        result['reasons'].append(f'missing evidence: {error.filename}')
    except (OSError, ValueError, KeyError, TypeError, AttributeError, StopIteration, IndexError) as error:
        result.update(status='FAIL', reasons=[str(error)])
    return result


def classification(output: Path, c: dict[str, Any]) -> tuple[str, list[str]]:
    if c['evidence_origin'] == 'synthetic':
        return 'synthetic_fixture', ['Fixture rates are invented test inputs, never measurements.']
    env = c['environment']
    if env['kind'] == 'vm':
        return 'vm_measurement', ['Virtual nodes do not establish DDR/HBM backing memory differences.']
    backing = env.get('backing', [])
    try:
        require(len(backing) == 2 and {b['type'] for b in backing} == {'DDR', 'HBM'}, 'both DDR/HBM types required')
        for i, item in enumerate(backing):
            proof = item['evidence']
            require(item['node'] == c['memory_nodes'][i] and proof in env['files'], 'backing node/evidence mismatch')
            path = output/proof
            require(path.resolve().is_relative_to((output/'environment').resolve()) and path.is_file()
                    and path.stat().st_size > 0 and item['verified_by'].strip(), 'missing operator-verified backing evidence')
        return 'verified_ddr_hbm', ['Backing types/mapping are operator-verified from the retained evidence; no speedup threshold applies.']
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        return 'hardware_numa_measurement', [f'DDR/HBM labels unavailable: {error}']


def summarize(output: Path, plan: dict[str, Any]) -> dict[str, Any]:
    c = plan['config']
    rows = [assess(output/'executions'/row['id'], c, row, load(output/row['manifest'])) for row in plan['executions']]
    baseline = next((row for row in rows if row['status'] == 'PASS'), None)
    for row in rows:
        if row['status'] == 'PASS' and baseline:
            for key in ('cpu_set', 'build', 'runtime_image', 'llc_bytes'):
                if row[key] != baseline[key]:
                    row['status'] = 'FAIL'
                    row['reasons'].append(f'uncontrolled {key} mismatch against {baseline["id"]}')
            peer = next(r for r in rows if r.get('worker_cpus') and r['threads'] == row['threads'])
            if row['worker_cpus'] != peer['worker_cpus']:
                row['status'] = 'FAIL'
                row['reasons'].append('uncontrolled worker CPU mapping mismatch')
    summaries = []
    for threads in c['threads']:
        for case in ('a', 'b'):
            valid = [row for row in rows if row['threads'] == threads and row['case'] == case
                     and row['phase'] == 'measured' and row['status'] == 'PASS']
            kernels = {}
            for kernel in KERNELS:
                values = [row['rates'][kernel]['best_rate'] for row in valid]
                if values:
                    kernels[kernel] = {'median':statistics.median(values), 'min':min(values), 'max':max(values), 'unit':'MB/s'}
            summaries.append({'threads':threads, 'case':case, 'count':len(valid), 'kernels':kernels})
    status = ('FAIL' if any(r['status'] == 'FAIL' for r in rows) else
              'NOT RUN' if any(r['status'] == 'NOT RUN' for r in rows) else
              'PASS_FIXTURE' if c['evidence_origin'] == 'synthetic' else
              'PASS_SMOKE' if c['mode'] == 'smoke' else 'PASS')
    label, notes = classification(output, c)
    if c['evidence_origin'] == 'live':
        try:
            for name in ('configz', 'node'):
                record = load(output/'preflight'/name/'command.json')
                require(record['status'] == 'completed' and record['returncode'] == 0 and record.get('finished'),
                        f'preflight {name} command failed/incomplete')
            policy = load(output/'preflight/configz/stdout.txt')['kubeletconfig']
            for key, value in {'cpuManagerPolicy':'static', 'memoryManagerPolicy':'Static',
                               'topologyManagerPolicy':'independent-numa', 'topologyManagerScope':'pod'}.items():
                require(policy.get(key) == value, f'kubelet {key} must be {value}')
            node = load(output/'preflight/node/stdout.txt')
            require(node['metadata']['name'] == c['node'] and node['status']['nodeInfo']['operatingSystem'] == 'linux',
                    'preflight node identity/Linux platform mismatch')
        except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
            notes.append(f'Live policy/platform preflight unavailable: {error}')
            if status != 'FAIL':
                status = 'NOT RUN'
    artifacts = {}
    for directory in ('executions', 'environment', 'manifests', 'preflight'):
        for path in sorted((output/directory).rglob('*')):
            if path.is_file():
                require(path.resolve().is_relative_to(output.resolve()), 'evidence symlink escapes output directory')
                # Avoid loading retained binaries/tar archives wholly into memory.
                artifacts[str(path.relative_to(output))] = file_hash(path)
    return {'schema_version':1, 'status':status, 'classification':label, 'notes':notes,
            'evidence_origin':c['evidence_origin'], 'mode':c['mode'], 'environment':c['environment'],
            'successful_controlled_comparison':status == 'PASS', 'executions':rows, 'summaries':summaries,
            'artifact_sha256':artifacts}
