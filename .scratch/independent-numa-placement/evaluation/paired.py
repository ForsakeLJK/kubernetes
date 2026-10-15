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

"""Prepare, execute, and report sequential paired STREAM Pods with retained evidence."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import time
from typing import Any

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT.parent / 'stream'))
from run import save

# Keep the collector alive after STREAM exits so kubectl can retrieve emptyDir evidence.
# It does not change memory policy; Kubernetes must supply both placement masks.
COLLECTOR = r'''
import json, pathlib, subprocess, sys, time
p = pathlib.Path('/evidence')
(p/'effective-status.txt').write_text(pathlib.Path('/proc/self/status').read_text())
c = json.loads(sys.argv[1])
(p/'node-cpus.txt').write_text(pathlib.Path('/sys/devices/system/node/node%d/cpulist' % c['cpu_node']).read_text())
cpus = __import__('os').sched_getaffinity(0)
caches = {}
for cpu in cpus:
    indices = list(pathlib.Path('/sys/devices/system/cpu/cpu%d/cache' % cpu).glob('index*'))
    last = max(int((i/'level').read_text()) for i in indices)
    for i in indices:
        if int((i/'level').read_text()) == last and (i/'type').read_text().strip() in ('Unified', 'Data'):
            shared = (i/'shared_cpu_list').read_text().strip()
            size = (i/'size').read_text().strip()
            caches[shared] = int(size[:-1]) * {'K':1024, 'M':1048576, 'G':1073741824}[size[-1]]
topology = {n.name+'/'+f.name: f.read_text() for n in pathlib.Path('/sys/devices/system/node').glob('node[0-9]*')
            for f in n.iterdir() if f.name in ('cpulist', 'meminfo', 'distance')}
(p/'host.json').write_text(json.dumps({'llc_bytes':sum(caches.values()), 'caches':caches,
    'kernel':__import__('platform').platform(), 'topology':topology}))
code = subprocess.call(c['argv'])
tmp = p/'execution.json.tmp'
tmp.write_text(json.dumps({'returncode':code}))
tmp.replace(p/'execution.json')
while True: time.sleep(60)
'''


def load(path: Path) -> Any:
    return json.loads(path.read_text())


def validated(c: dict[str, Any]) -> dict[str, Any]:
    for name in ('node', 'namespace'):
        if not isinstance(c[name], str) or not re.fullmatch(r'[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?', c[name]):
            raise ValueError(f'{name}: supply an actual Kubernetes name')
    if not re.fullmatch(r'[^\s]+@sha256:[0-9a-f]{64}', c['image']):
        raise ValueError('image must use NAME@sha256:DIGEST')
    nodes = c['memory_nodes']
    if (not isinstance(nodes, list) or len(nodes) != 2 or nodes[0] == nodes[1]
            or any(type(n) is not int or not 0 <= n <= 63 for n in [c['cpu_node'], *nodes])):
        raise ValueError('cpu_node and two distinct memory_nodes must be IDs in 0..63')
    bounds = {'cpu': (1, 1048576), 'array_size': (1, (2**63-1)//24),
              'llc_bytes': (1, 2**63-1), 'overhead_bytes': (1, 2**63-1),
              'iterations': (2, 100), 'measured_runs': (5, 1000),
              'observe_seconds': (0, 3600), 'residency_samples': (1, 1000000),
              'timeout': (1, 86400)}
    for name, (low, high) in bounds.items():
        if type(c[name]) is not int or not low <= c[name] <= high:
            raise ValueError(f'{name} must be an integer in {low}..{high}')
    if c['timeout'] <= c['observe_seconds']:
        raise ValueError('timeout must exceed observe_seconds')
    threads = c['threads']
    if (not isinstance(threads, list) or not threads or len(set(threads)) != len(threads)
            or 1 not in threads or any(type(t) is not int or not 1 <= t <= c['cpu'] for t in threads)):
        raise ValueError('threads must include 1 and distinct counts <= allocated cpu; no oversubscription')
    if c['mode'] not in ('smoke', 'bandwidth') or c['evidence_origin'] not in ('live', 'synthetic'):
        raise ValueError('mode must be smoke/bandwidth; evidence_origin must be live/synthetic')
    if c['mode'] == 'bandwidth' and (c['array_size'] < 1000000 or c['array_size'] * 8 < 4*c['llc_bytes']):
        raise ValueError('array_size must be >=1000000 doubles and >=4 times llc_bytes per array')
    if not re.fullmatch(r'[1-9][0-9]*(?:Ki|Mi|Gi)', c['memory']):
        raise ValueError('memory must be a positive Ki/Mi/Gi quantity')
    memory = int(c['memory'][:-2]) * {'Ki':1024, 'Mi':1024**2, 'Gi':1024**3}[c['memory'][-2:]]
    available = c['target_available_bytes']
    if (not isinstance(available, list) or len(available) != 2
            or any(type(n) is not int or n <= 0 for n in available)
            or c['array_size']*24 + c['overhead_bytes'] > min(memory, *available)):
        raise ValueError('three arrays plus overhead_bytes must fit the container limit and both target_available_bytes')
    if not isinstance(c['cflags'], list) or any(not isinstance(f, str) for f in c['cflags']):
        raise ValueError('cflags must be an array of compiler flags')
    if any(f.startswith(('-D', '-U', '-Wp,', '-Xpreprocessor', '-fno-openmp')) for f in c['cflags']):
        raise ValueError('cflags cannot override workload definitions or disable OpenMP')
    env = c['environment']
    if env['kind'] not in ('vm', 'physical'):
        raise ValueError('environment.kind must be vm or physical')
    for key in ('description', 'interference', 'settings', 'fork_commit'):
        if not isinstance(env[key], str) or not env[key].strip():
            raise ValueError(f'environment.{key} must record the actual setting, or an explicit unknown')
    if not isinstance(env['files'], list):
        raise ValueError('environment.files must list supporting topology/configuration evidence paths')
    return c


def workload(c: dict[str, Any], threads: int) -> list[str]:
    argv = ['python3', '/opt/stream/run.py', '--output', '/evidence/run', '--image-identity', c['image']]
    for flag, key in [('array-size', 'array_size'), ('iterations', 'iterations'),
                      ('observe-seconds', 'observe_seconds'), ('residency-samples', 'residency_samples'),
                      ('timeout', 'timeout'), ('mode', 'mode'), ('llc-bytes', 'llc_bytes')]:
        argv += ['--'+flag, str(c[key])]
    return argv + ['--threads', str(threads)] + ['--cflag='+f for f in c['cflags']]


def make_plan(c: dict[str, Any], token: str) -> dict[str, Any]:
    executions = []
    for threads in c['threads']:
        for repeat in range(c['measured_runs']+1):
            for case in (['a', 'b'] if repeat % 2 == 0 else ['b', 'a']):
                identifier = f't{threads}-{repeat:03d}-{case}'
                executions.append({'id':identifier, 'case':case, 'threads':threads,
                                   'phase':'warmup' if repeat == 0 else 'measured',
                                   'pod':f'stream-{token}-{identifier}',
                                   'manifest':f'manifests/{identifier}.json'})
    return {'schema_version':1, 'config':c, 'token':token, 'executions':executions}


def manifest(c: dict[str, Any], row: dict[str, Any]) -> dict[str, Any]:
    resources = {'cpu':str(c['cpu']), 'memory':c['memory']}
    return {'apiVersion':'v1', 'kind':'Pod', 'metadata':{'name':row['pod'], 'namespace':c['namespace']},
            'spec':{'nodeName':c['node'], 'cpuNUMA':c['cpu_node'],
                    'memNUMA':c['memory_nodes'][0 if row['case'] == 'a' else 1],
                    'restartPolicy':'Never', 'terminationGracePeriodSeconds':1,
                    'containers':[{'name':'stream', 'image':c['image'], 'imagePullPolicy':'IfNotPresent',
                        'command':['python3', '-u', '-c', COLLECTOR,
                                   json.dumps({'cpu_node':c['cpu_node'], 'argv':workload(c, row['threads'])})],
                        'resources':{'requests':resources, 'limits':resources},
                        'volumeMounts':[{'name':'evidence', 'mountPath':'/evidence'}]}],
                    'volumes':[{'name':'evidence', 'emptyDir':{}}]}}


def prepare(config: Path, output: Path) -> int:
    c = validated(load(config))
    output.mkdir(parents=True, exist_ok=False)
    (output/'manifests').mkdir()
    (output/'environment').mkdir()
    # Evidence references become self-contained and cannot point outside the archive.
    for index, name in enumerate(c['environment']['files']):
        source = (config.parent / name).resolve()
        target = f'environment/{index:03d}-{source.name}'
        (output/target).write_bytes(source.read_bytes())
        c['environment']['files'][index] = target
        for backing in c['environment'].get('backing', []):
            if backing.get('evidence') == name:
                backing['evidence'] = target
    plan = make_plan(c, hashlib.sha256(str(output.resolve()).encode()).hexdigest()[:8])
    for row in plan['executions']:
        save(output/row['manifest'], manifest(c, row))
    save(output/'plan.json', plan)
    print(json.dumps({'status':'prepared', 'executions':len(plan['executions']), 'output':str(output)}))
    return 0


def capture(directory: Path, label: str, command: list[str], timeout: int) -> subprocess.CompletedProcess:
    target = directory/label
    subprocess.run([sys.executable, str(ROOT.parent/'runtime/capture.py'),
                    '--output', str(target), '--timeout', str(timeout), '--', *command], capture_output=True)
    record = load(target/'command.json')
    return subprocess.CompletedProcess(command, record.get('returncode') if record.get('returncode') is not None else 1,
                                       (target/'stdout.txt').read_bytes(), (target/'stderr.txt').read_bytes())


def checked_plan(output: Path) -> dict[str, Any]:
    plan = load(output/'plan.json')
    c = validated(plan['config'])
    if plan != make_plan(c, plan['token']) or not re.fullmatch('[0-9a-f]{8}', plan['token']):
        raise ValueError('plan schedule is inconsistent; prepare a new evaluation')
    for row in plan['executions']:
        if load(output/row['manifest']) != manifest(c, row):
            raise ValueError(f"{row['id']}: manifest differs from the prepared experiment")
    return plan


def run(output: Path, kubectl: str) -> int:
    plan = checked_plan(output)
    c = plan['config']
    if c['evidence_origin'] != 'live':
        raise ValueError('run requires evidence_origin=live; synthetic fixtures cannot contact a cluster')
    executions = output/'executions'
    executions.mkdir(exist_ok=False)
    preflight = output/'preflight'
    preflight.mkdir()
    configz = capture(preflight, 'configz', [kubectl, 'get', '--raw', f"/api/v1/nodes/{c['node']}/proxy/configz"], 30)
    if configz.returncode:
        raise ValueError('cannot read kubelet configz; inspect preflight/configz and grant node proxy read access')
    policy = json.loads(configz.stdout)['kubeletconfig']
    for key, value in {'cpuManagerPolicy':'static', 'memoryManagerPolicy':'Static',
                       'topologyManagerPolicy':'independent-numa', 'topologyManagerScope':'pod'}.items():
        if policy.get(key) != value:
            raise ValueError(f'kubelet {key} must be {value}; inspect preflight/configz')
    node = capture(preflight, 'node', [kubectl, 'get', 'node', c['node'], '-o', 'json'], 30)
    if node.returncode:
        raise ValueError('cannot capture node identity; inspect preflight/node')
    for row in plan['executions']:
        directory = executions/row['id']
        directory.mkdir()
        base = [kubectl, '--namespace', c['namespace']]
        created = False
        failure = None
        try:
            create = capture(directory, 'create', base+['create', '-f', str(output/row['manifest'])], c['timeout'])
            if create.returncode:
                raise ValueError('Pod creation failed; inspect create/stderr.txt')
            created = True
            deadline = time.monotonic()+c['timeout']+120
            poll = 0
            while True:
                state = capture(directory, f'poll-{poll:04d}', base+['get', 'pod', row['pod'], '-o', 'json'], 30)
                pod = json.loads(state.stdout) if state.returncode == 0 else {}
                if pod.get('status', {}).get('phase') in ('Failed', 'Succeeded'):
                    raise ValueError('collector terminated; inspect Pod status for OOM/exit/admission failure')
                ready = capture(directory, f'completion-{poll:04d}', base+['exec', row['pod'], '-c', 'stream',
                                 '--', 'cat', '/evidence/execution.json'], 30)
                if ready.returncode == 0:
                    break
                if time.monotonic() >= deadline:
                    raise ValueError('execution timed out; inspect polling, events and workload logs')
                poll += 1
                time.sleep(2)
            archive = capture(directory, 'archive', base+['exec', row['pod'], '-c', 'stream', '--',
                              'tar', 'cf', '-', '-C', '/evidence', '.'], 60)
            if archive.returncode:
                raise ValueError('evidence retrieval failed; inspect archive/stderr.txt')
            with tarfile.open(directory/'archive/stdout.txt') as tar:
                tar.extractall(directory/'workload', filter='data')
        except (OSError, ValueError, tarfile.TarError) as error:
            failure = str(error)
        finally:
            for label, command in [('pod', ['get', 'pod', row['pod'], '-o', 'json']),
                                    ('logs', ['logs', row['pod'], '-c', 'stream']),
                                    ('events', ['get', 'events', '--field-selector', 'involvedObject.name='+row['pod'], '-o', 'json'])]:
                capture(directory, label, base+command, 30)
            if created:
                deleted = capture(directory, 'delete', base+['delete', 'pod', row['pod'], '--wait=true', '--timeout=120s'], 130)
                if deleted.returncode:
                    failure = (failure+'; ' if failure else '')+'Pod cleanup failed; remove the recorded Pod before retrying'
        save(directory/'orchestration.json', {'status':'failed' if failure else 'completed',
                                             'error':failure, 'evidence_origin':'live'})
        if failure:
            print(f"{row['id']}: {failure}", file=sys.stderr)
            break
    return report(output)


def report(output: Path) -> int:
    from evidence import summarize
    result = summarize(output, checked_plan(output))
    save(output/'report.json', result)
    lines = [f"Paired STREAM: {result['status']}", f"Classification: {result['classification']}",
             f"Evidence: {result['evidence_origin']}; mode: {result['mode']}",
             'Rates: decimal MB/s; median and [minimum, maximum] of independent execution best rates.',
             'Page placement describes sampled initialized arrays before timed kernels.', '']
    for group in result['summaries']:
        lines.append(f"Threads {group['threads']}, case {group['case']}, n={group['count']}")
        for kernel, values in group['kernels'].items():
            lines.append(f"  {kernel}: {values['median']} [{values['min']}, {values['max']}] MB/s")
    lines += ['', *[f"{row['id']}: {row['status']} — {'; '.join(row['reasons'])}" for row in result['executions']]]
    (output/'report.txt').write_text('\n'.join(lines)+'\n')
    print(json.dumps({'status':result['status'], 'report':str(output/'report.json')}))
    return 0 if result['status'] in ('PASS', 'PASS_FIXTURE', 'PASS_SMOKE') else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    for name in ('prepare', 'run', 'report'):
        command = commands.add_parser(name)
        command.add_argument('--output', required=True, type=Path)
        if name == 'prepare':
            command.add_argument('--config', required=True, type=Path)
        if name == 'run':
            command.add_argument('--kubectl', default='kubectl', help='executable, not a shell command')
    args = parser.parse_args()
    try:
        if args.command == 'prepare':
            return prepare(args.config, args.output)
        return run(args.output, args.kubectl) if args.command == 'run' else report(args.output)
    except (OSError, ValueError, KeyError, TypeError, AttributeError, tarfile.TarError) as error:
        print(f'{args.command}: {error}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
