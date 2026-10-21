#!/usr/bin/env python3
"""User-operated compatibility, API restriction and absent-node checks."""
import copy
import datetime
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

BASE = Path('/home/jukeliu/k8s_workspace')
NS = 'numa-validation'
CHECKER = BASE / 'kubernetes/.scratch/explicit-numa-placement/runtime/check-masks.py'


def manifest(name, fields):
    return {
        'apiVersion': 'v1', 'kind': 'Pod',
        'metadata': {'name': name, 'namespace': NS},
        'spec': {'nodeName': 'ubuntu-numa-test', **fields, 'containers': [{
            'name': 'app', 'image': 'busybox:1.36',
            'command': ['sh', '-c', 'sleep 86400'],
            'resources': {'requests': {'cpu': '1', 'memory': '128Mi'},
                          'limits': {'cpu': '1', 'memory': '128Mi'}},
        }]},
    }


def main():
    run = Path((Path.home() / '.numa-ticket11-current').read_text().strip())
    out = Path(tempfile.mkdtemp(prefix='compatibility-api.', dir=run))
    os.environ['PATH'] = str(BASE / 'ticket11-bin') + os.pathsep + os.environ['PATH']
    os.environ['KUBECONFIG'] = str(run / 'certs/admin.kubeconfig')
    results = []
    print('Evidence directory:', out, flush=True)

    def command(label, argv, timeout=210):
        folder = out / label
        folder.mkdir()
        record = {'argv': argv, 'started_utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        try:
            process = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
            (folder / 'stdout.txt').write_text(process.stdout)
            (folder / 'stderr.txt').write_text(process.stderr)
            record['exit_code'] = process.returncode
        except subprocess.TimeoutExpired as error:
            for name, content in [('stdout', error.stdout), ('stderr', error.stderr)]:
                (folder / f'{name}.txt').write_bytes(
                    content if isinstance(content, bytes) else (content or '').encode())
            record['timed_out'] = True
            raise
        finally:
            record['finished_utc'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            (folder / 'command.json').write_text(json.dumps(record, indent=2))
        return process

    def check(label, passed, detail=''):
        results.append({'case': label, 'result': 'PASS' if passed else 'FAIL', 'detail': detail})
        print(results[-1]['result'], label, flush=True)
        if not passed:
            raise RuntimeError(f'{label}: {detail}')

    def successful(label, argv):
        p = command(label, argv)
        check(label, p.returncode == 0, p.stderr.strip())
        return p

    def rejected(label, argv, required, optional_api=False):
        p = command(label, argv)
        text = p.stdout + p.stderr
        if optional_api and p.returncode and (
                'the server could not find the requested resource' in text
                or 'does not allow this method on the requested resource' in text):
            results.append({'case': label, 'result': 'NOT RUN', 'detail': text.strip()})
            print('NOT RUN', label, '(subresource unavailable)', flush=True)
            return
        check(label, p.returncode != 0 and all(s in text for s in required), text.strip())

    def save(name, value):
        path = out / f'{name}.json'
        path.write_text(json.dumps(value, indent=2))
        return path

    def patch(label, pod, spec, required, subresource=None):
        patch_type = '--type=strategic' if subresource == 'resize' else '--type=merge'
        argv = ['kubectl', '-n', NS, 'patch', 'pod', pod, patch_type,
                '--dry-run=server', '-p', json.dumps({'spec': spec})]
        if subresource:
            argv.append('--subresource=' + subresource)
        rejected(label, argv, required, optional_api=bool(subresource))

    try:
        # Dry-run requests distinguish feature validation from unrelated errors.
        invalid = []
        for field in ['cpuNUMA', 'memNUMA', 'numaNode']:
            for value in [-1, 64]:
                invalid.append((f'invalid-{field.lower()}-{abs(value)}',
                                manifest('invalid-placeholder', {field: value}),
                                [field, 'must be between 0 and 63']))
        for field in ['cpuNUMA', 'memNUMA']:
            invalid.append((f'mixed-{field.lower()}',
                            manifest('invalid-placeholder', {'numaNode': 0, field: 0}),
                            ['numaNode cannot be combined']))
        for role in ['app', 'init', 'sidecar']:
            p = manifest('invalid-placeholder', {'cpuNUMA': 0, 'memNUMA': 1})
            c = p['spec']['containers'][0]
            if role != 'app':
                c = copy.deepcopy(c)
                c['name'] = role
                if role == 'sidecar':
                    c['restartPolicy'] = 'Always'
                p['spec']['initContainers'] = [c]
            c['resources']['requests']['cpu'] = '500m'
            c['resources']['limits']['cpu'] = '500m'
            invalid.append((f'fractional-{role}', p, ['CPU request must be a positive whole number']))
            p = copy.deepcopy(p)
            c = (p['spec']['containers'] if role == 'app' else p['spec']['initContainers'])[0]
            c['resources']['requests']['cpu'] = c['resources']['limits']['cpu'] = '1'
            c['resources']['requests']['memory'] = '64Mi'
            invalid.append((f'unequal-memory-{role}', p,
                            ['memory request and limit must be present and equal']))
        for label, pod, expected in invalid:
            pod['metadata']['name'] = label
            path = save(label, pod)
            rejected(label, ['kubectl', 'create', '--dry-run=server', '-f', str(path)], expected)

        # Sequential Pods avoid artificial CPU pressure between compatibility cases.
        forms = [
            ('equal', {'cpuNUMA': 0, 'memNUMA': 0}, 0, 0),
            ('legacy', {'numaNode': 0}, 0, 0),
            ('ordinary', {}, None, None),
            ('explicit', {'cpuNUMA': 0, 'memNUMA': 1}, 0, 1),
            ('cpu-only', {'cpuNUMA': 0}, 0, 0),
            ('memory-only', {'memNUMA': 1}, 1, 1),
        ]
        for form, fields, cpu, mem in forms:
            name = 'compat-' + form
            path = save(name, manifest(name, fields))
            successful(form + '-create', ['kubectl', 'create', '-f', str(path)])
            successful(form + '-ready', ['kubectl', '-n', NS, 'wait', '--for=condition=Ready',
                                        'pod/' + name, '--timeout=180s'])
            successful(form + '-pod-before', ['kubectl', '-n', NS, 'get', 'pod', name, '-o', 'json'])

            if not fields:
                p = successful(form + '-raw-status', ['kubectl', '-n', NS, 'exec', name,
                                                     '-c', 'app', '--', 'cat', '/proc/1/status'])
                sys.path.insert(0, str(CHECKER.parent))
                from masklib import status_lists, parse_list
                cpus, mems = status_lists(p.stdout)
                nodes = []
                for directory in Path('/sys/devices/system/node').glob('node[0-9]*'):
                    eligible = parse_list((directory / 'cpulist').read_text().strip())
                    if cpus and cpus <= eligible:
                        nodes.append(int(directory.name[4:]))
                check('ordinary-common-node', len(nodes) == 1 and mems == {nodes[0]},
                      f'CPU nodes={nodes}, memory nodes={sorted(mems)}')
                cpu = mem = nodes[0]

            cpulist = Path(f'/sys/devices/system/node/node{cpu}/cpulist').read_text().strip()
            successful(form + '-masks-before', ['python3', str(CHECKER), '--namespace', NS,
                       '--pod', name, '--container', 'app', '--cpu-node', str(cpu),
                       '--memory-node', str(mem), '--node-cpus', cpulist,
                       '--output', str(out / f'{form}-masks-before.json')])
            for field, value in fields.items():
                patch(f'{form}-change-{field}', name, {field: 1 - value}, [field + ' is immutable'])
                patch(f'{form}-remove-{field}', name, {field: None}, [field + ' is immutable'])
            additions = ({'cpuNUMA': cpu, 'memNUMA': mem, 'numaNode': cpu} if not fields else
                         {'memNUMA': mem} if form == 'cpu-only' else
                         {'cpuNUMA': cpu} if form == 'memory-only' else {})
            for field, value in additions.items():
                patch(f'{form}-add-{field}', name, {field: value}, [field + ' is immutable'])
            if fields:
                successful(form + '-unchanged', ['kubectl', '-n', NS, 'patch', 'pod', name,
                           '--type=merge', '-p', json.dumps({'spec': fields})])
            successful(form + '-annotation', ['kubectl', '-n', NS, 'annotate', 'pod', name,
                                              'numa-validation=updated', '--overwrite'])
            if form == 'explicit':
                patch('resize-rejection', name, {'containers': [{
                    'name': 'app', 'resources': {'requests': {'cpu': '2', 'memory': '128Mi'},
                                                'limits': {'cpu': '2', 'memory': '128Mi'}}}]},
                    ['CPU or memory resize is not supported with NUMA placement'], 'resize')
                patch('ephemeral-rejection', name, {'ephemeralContainers': [{
                    'name': 'debug', 'image': 'busybox:1.36', 'command': ['sleep', '60']}]},
                    ['ephemeral containers are not supported with NUMA placement'], 'ephemeralcontainers')
            successful(form + '-masks-after', ['python3', str(CHECKER), '--namespace', NS,
                       '--pod', name, '--container', 'app', '--cpu-node', str(cpu),
                       '--memory-node', str(mem), '--node-cpus', cpulist,
                       '--output', str(out / f'{form}-masks-after.json')])
            successful(form + '-pod-after', ['kubectl', '-n', NS, 'get', 'pod', name, '-o', 'json'])
            successful(form + '-delete', ['kubectl', '-n', NS, 'delete', 'pod', name,
                                         '--wait=true', '--timeout=120s'])

        for field, resource in [('cpuNUMA', 'CPU'), ('memNUMA', 'memory')]:
            name = 'absent-' + field.lower()
            fields = {'cpuNUMA': 0, 'memNUMA': 1, field: 63}
            check(name + '-topology', not Path('/sys/devices/system/node/node63').exists())
            path = save(name, manifest(name, fields))
            successful(name + '-create', ['kubectl', 'create', '-f', str(path)])
            successful(name + '-failed', ['kubectl', '-n', NS, 'wait',
                       '--for=jsonpath={.status.phase}=Failed', 'pod/' + name, '--timeout=90s'])
            p = successful(name + '-pod', ['kubectl', '-n', NS, 'get', 'pod', name, '-o', 'json'])
            status = json.loads(p.stdout)['status']
            check(name + '-reason', status.get('reason') == 'NUMANodeNotFound'
                  and f'{resource} NUMA node 63 is absent' in status.get('message', ''), str(status))
            successful(name + '-delete', ['kubectl', '-n', NS, 'delete', 'pod', name,
                                         '--wait=true', '--timeout=120s'])
    finally:
        (out / 'results.json').write_text(json.dumps(results, indent=2))
        counts = {state: sum(r['result'] == state for r in results) for state in ['PASS', 'FAIL', 'NOT RUN']}
        print('Recorded checks:', counts, flush=True)
        print('Evidence saved to:', out, flush=True)
    print('Batch complete. Masks and API behavior tested; page residency and old-policy compatibility remain separate.')


if __name__ == '__main__':
    main()
