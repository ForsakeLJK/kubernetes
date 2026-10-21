#!/usr/bin/env python3
"""Run the existing query-only C probe as PID 1 in test containers."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

BASE = Path('/home/jukeliu/k8s_workspace')
REPO = BASE / 'kubernetes'
RUNTIME = REPO / '.scratch/independent-numa-placement/runtime'
MASKS = REPO / '.scratch/explicit-numa-placement/runtime/check-masks.py'
NS = 'numa-validation'


def main():
    run = Path((Path.home() / '.numa-ticket11-current').read_text().strip())
    out = Path(tempfile.mkdtemp(prefix='page-residency.', dir=run))
    os.environ['PATH'] = str(BASE / 'ticket11-bin') + os.pathsep + os.environ['PATH']
    os.environ['KUBECONFIG'] = str(run / 'certs/admin.kubeconfig')
    print('Evidence directory:', out, flush=True)
    counter = 0
    results = []

    def command(label, argv, check=True):
        nonlocal counter
        counter += 1
        folder = out / f'{counter:03d}-{label}'
        folder.mkdir()
        record = {'argv': argv, 'started_utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        try:
            p = subprocess.run(argv, capture_output=True, text=True, timeout=210)
            (folder / 'stdout.txt').write_text(p.stdout)
            (folder / 'stderr.txt').write_text(p.stderr)
            record['exit_code'] = p.returncode
        except subprocess.TimeoutExpired as error:
            for name, content in [('stdout', error.stdout), ('stderr', error.stderr)]:
                (folder / f'{name}.txt').write_bytes(
                    content if isinstance(content, bytes) else (content or '').encode())
            record['timed_out'] = True
            raise
        finally:
            record['finished_utc'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            (folder / 'command.json').write_text(json.dumps(record, indent=2))
        if check and p.returncode:
            raise RuntimeError(f'{label}: {p.stderr.strip()} {p.stdout.strip()}')
        return p

    def kubectl(label, args, check=True):
        return command(label, ['kubectl', '-n', NS, *args], check)

    try:
        source = out / 'probe.c'
        shutil.copyfile(RUNTIME / 'probe.c', source)
        binary = out / 'numa-probe'
        command('compiler-version', ['gcc', '--version'])
        command('build-static-probe', ['gcc', '-static', '-std=c99', '-D_GNU_SOURCE',
                '-Wall', '-Wextra', '-Werror', str(source), '-o', str(binary)])
        # Retain exact bytes alongside the compiler command and version.
        hashes = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in [source, binary]}
        (out / 'build-hashes.json').write_text(json.dumps(hashes, indent=2))
        sys.path.insert(0, str(MASKS.parent))
        from masklib import status_lists, parse_list

        for form, fields, cpu, mem in [
            ('explicit', {'cpuNUMA': 0, 'memNUMA': 1}, 0, 1),
            ('reverse', {'cpuNUMA': 1, 'memNUMA': 0}, 1, 0),
            ('cpu-only', {'cpuNUMA': 0}, 0, 0),
            ('memory-only', {'memNUMA': 1}, 1, 1),
            ('equal', {'cpuNUMA': 0, 'memNUMA': 0}, 0, 0),
            ('legacy', {'numaNode': 0}, 0, 0),
            ('ordinary', {}, None, None),
        ]:
            name = 'residency-' + form
            case = out / form
            case.mkdir()
            pod = {
                'apiVersion': 'v1', 'kind': 'Pod',
                'metadata': {'name': name, 'namespace': NS},
                'spec': {'nodeName': 'ubuntu-numa-test', **fields, 'containers': [{
                    'name': 'app', 'image': 'busybox:1.36',
                    'command': ['sh', '-c',
                        'while [ ! -x /tmp/numa-probe ]; do sleep 1; done; '
                        'exec /tmp/numa-probe --container app --bytes 16777216 --hold-seconds 3600'],
                    'env': [{'name': 'NUMA_POD_NAME', 'value': name},
                            {'name': 'NUMA_NAMESPACE', 'value': NS}],
                    'resources': {'requests': {'cpu': '1', 'memory': '128Mi'},
                                  'limits': {'cpu': '1', 'memory': '128Mi'}},
                }]},
            }
            manifest = case / 'manifest.json'
            manifest.write_text(json.dumps(pod, indent=2))
            kubectl(form + '-create', ['create', '-f', str(manifest)])
            kubectl(form + '-ready', ['wait', '--for=condition=Ready', 'pod/' + name, '--timeout=180s'])
            kubectl(form + '-copy-binary', ['cp', str(binary), name + ':/tmp/numa-probe.copy', '-c', 'app'])
            copied = kubectl(form + '-binary-hash', ['exec', name, '-c', 'app', '--',
                                                      'sha256sum', '/tmp/numa-probe.copy'])
            assert copied.stdout.split()[0] == hashes['numa-probe'], 'Copied probe binary hash differs'
            kubectl(form + '-start-probe', ['exec', name, '-c', 'app', '--', 'sh', '-c',
                    'chmod +x /tmp/numa-probe.copy && mv /tmp/numa-probe.copy /tmp/numa-probe'])
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline:
                p = kubectl(form + '-probe-poll', ['exec', name, '-c', 'app', '--',
                            'test', '-s', '/tmp/numa-evidence/numa_maps.txt'], check=False)
                if p.returncode == 0:
                    break
                time.sleep(2)
            else:
                kubectl(form + '-describe', ['describe', 'pod', name], check=False)
                kubectl(form + '-logs', ['logs', name, '-c', 'app'], check=False)
                raise RuntimeError(f'{form}: probe evidence did not appear; inspect saved logs')
            kubectl(form + '-copy-evidence', ['cp', name + ':/tmp/numa-evidence', str(case / 'probe'), '-c', 'app'])
            status_file = case / 'probe/status.txt'
            if not fields:
                cpus, mems = status_lists(status_file.read_text())
                nodes = [int(d.name[4:]) for d in Path('/sys/devices/system/node').glob('node[0-9]*')
                         if cpus and cpus <= parse_list((d / 'cpulist').read_text().strip())]
                assert len(nodes) == 1 and mems == {nodes[0]}, f'Ordinary Pod lacks common-node masks: {nodes}, {mems}'
                cpu = mem = nodes[0]
            cpulist = Path(f'/sys/devices/system/node/node{cpu}/cpulist').read_text().strip()
            command(form + '-masks', ['python3', str(MASKS), '--namespace', NS, '--pod', name,
                    '--container', 'app', '--cpu-node', str(cpu), '--memory-node', str(mem),
                    '--node-cpus', cpulist, '--status-file', str(status_file),
                    '--output', str(case / 'masks.json')])
            p = command(form + '-residency', ['python3', str(RUNTIME / 'check-residency.py'),
                        str(case / 'probe/observation.json'), '--memory-node', str(mem),
                        '--output', str(case / 'residency.json')], check=False)
            result = json.loads((case / 'residency.json').read_text())
            assert result.get('pod') == name and result.get('namespace') == NS, 'Probe identity mismatch'
            pod = kubectl(form + '-pod', ['get', 'pod', name, '-o', 'json'])
            (case / 'pod.json').write_text(pod.stdout)
            results.append({'case': form, 'mask_status': 'PASS', 'residency_status': result['status'],
                            'cpu_node': cpu, 'memory_node': mem, 'reasons': result.get('reasons', [])})
            print(f"{form}: masks PASS; residency {result['status']} (CPU node {cpu}, memory node {mem})", flush=True)
            if result.get('reasons'):
                print('  ' + '; '.join(result['reasons']), flush=True)
            if p.returncode not in [0, 2] or result['status'] == 'FAIL':
                raise RuntimeError(f'{form}: residency failure; Pod and raw evidence retained')
            kubectl(form + '-delete', ['delete', 'pod', name, '--wait=true', '--timeout=120s'])
    finally:
        (out / 'results.json').write_text(json.dumps(results, indent=2))
        counts = {state: sum(r['residency_status'] == state for r in results)
                  for state in ['PASS', 'FAIL', 'NOT RUN']}
        print('Residency checks:', counts, flush=True)
        print('Evidence saved to:', out, flush=True)


if __name__ == '__main__':
    main()
