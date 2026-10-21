#!/usr/bin/env python3
"""Restart only the current test kubelet; retain its root, flags and checkpoints."""
import datetime
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

BASE = Path('/home/jukeliu/k8s_workspace')
BIN = BASE / 'ticket11-bin'
CHECKER = BASE / 'kubernetes/.scratch/explicit-numa-placement/runtime/check-masks.py'
NS = 'numa-validation'
FORMS = [('explicit', {'cpuNUMA': 0, 'memNUMA': 1}, 0, 1),
         ('cpu-only', {'cpuNUMA': 0}, 0, 0),
         ('memory-only', {'memNUMA': 1}, 1, 1)]


def main():
    run = Path((Path.home() / '.numa-ticket11-current').read_text().strip())
    out = Path(tempfile.mkdtemp(prefix='kubelet-recovery.', dir=run))
    os.environ['PATH'] = str(BIN) + os.pathsep + os.environ['PATH']
    os.environ['KUBECONFIG'] = str(run / 'certs/admin.kubeconfig')
    print('Evidence directory:', out, flush=True)
    if (run / 'restarted-kubelet-unit.txt').exists():
        raise SystemExit('This run already has a restarted kubelet unit. Inspect it before repeating this batch.')
    subprocess.run(['sudo', '-v'], check=True)
    results = []
    counter = 0

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

    def kubectl(label, args):
        return command(label, ['kubectl', '-n', NS, *args])

    def check(label, condition, detail=''):
        results.append({'case': label, 'result': 'PASS' if condition else 'FAIL', 'detail': detail})
        print(results[-1]['result'], label, flush=True)
        if not condition:
            raise RuntimeError(f'{label}: {detail}')

    def pod_json(name, phase):
        p = kubectl(name + '-' + phase, ['get', 'pod', name, '-o', 'json'])
        (out / f'{name}-{phase}.json').write_text(p.stdout)
        return json.loads(p.stdout)

    def masks(form, cpu, mem, phase):
        name = 'recovery-' + form
        cpus = Path(f'/sys/devices/system/node/node{cpu}/cpulist').read_text().strip()
        command(form + '-masks-' + phase, ['python3', str(CHECKER), '--namespace', NS,
                '--pod', name, '--container', 'app', '--cpu-node', str(cpu),
                '--memory-node', str(mem), '--node-cpus', cpus,
                '--output', str(out / f'{form}-masks-{phase}.json')])
        check(form + '-masks-' + phase, True)

    def snapshots(phase, root):
        states = {}
        for manager in ['cpu', 'memory']:
            p = command(manager + '-checkpoint-' + phase,
                        ['sudo', 'cat', str(root / f'{manager}_manager_state')])
            (out / f'{manager}-checkpoint-{phase}.json').write_text(p.stdout)
            states[manager] = json.loads(p.stdout)
            if isinstance(states[manager].get('data'), str):
                states[manager] = json.loads(states[manager]['data'])
        return states

    unit = 'numa-ticket11-' + out.name.replace('.', '-') + '.service'
    try:
        # Inspect only the current kubelet's executable/argv/cwd, never its environment.
        pids = command('find-kubelet', ['pgrep', '-x', 'kubelet']).stdout.split()
        if len(pids) != 1:
            raise RuntimeError(f'Expected one running kubelet, found {pids}')
        pid = int(pids[0])
        script = ('import json,os,pathlib,sys; p=pathlib.Path("/proc")/sys.argv[1]; '
                  'print(json.dumps({"exe":os.readlink(p/"exe"),"cwd":os.readlink(p/"cwd"),'
                  '"argv":[x.decode() for x in (p/"cmdline").read_bytes().split(bytes([0])) if x]}))')
        detail = json.loads(command('original-process', ['sudo', 'python3', '-c', script, str(pid)]).stdout)
        (out / 'original-kubelet.json').write_text(json.dumps(detail, indent=2))
        argv = detail['argv']
        if Path(detail['exe']).resolve() != (BIN / 'kubelet').resolve():
            raise RuntimeError('Running kubelet differs from the ticket11 binary; no process was stopped')

        def flag(name):
            for index, value in enumerate(argv):
                if value.startswith(name + '='):
                    return value.split('=', 1)[1]
                if value == name:
                    return argv[index + 1]
            raise RuntimeError(f'Missing original kubelet flag {name}')

        root = Path(flag('--root-dir'))
        if root != Path('/var/lib') / ('kubelet-' + run.name):
            raise RuntimeError(f'Unexpected test kubelet root {root}; no process was stopped')
        if Path(flag('--config')).resolve() != (run / 'config/kubelet.yaml').resolve():
            raise RuntimeError('Unexpected kubelet configuration; no process was stopped')

        before = {}
        for form, fields, cpu, mem in FORMS:
            name = 'recovery-' + form
            pod = {'apiVersion': 'v1', 'kind': 'Pod',
                   'metadata': {'name': name, 'namespace': NS},
                   'spec': {'nodeName': 'ubuntu-numa-test', **fields, 'containers': [{
                       'name': 'app', 'image': 'busybox:1.36', 'command': ['sh', '-c', 'sleep 86400'],
                       'resources': {'requests': {'cpu': '1', 'memory': '128Mi'},
                                     'limits': {'cpu': '1', 'memory': '128Mi'}}}]}}
            path = out / f'{form}-manifest.json'
            path.write_text(json.dumps(pod, indent=2))
            kubectl(form + '-create', ['create', '-f', str(path)])
            kubectl(form + '-ready-before', ['wait', '--for=condition=Ready', 'pod/' + name, '--timeout=180s'])
            before[form] = pod_json(name, 'before-kubelet')
            masks(form, cpu, mem, 'before-kubelet')
        original = snapshots('before-kubelet', root)

        print('Restarting only kubelet; retaining the current root, configuration and checkpoints.', flush=True)
        command('stop-original-kubelet', ['sudo', 'kill', '-TERM', str(pid)])
        deadline = time.monotonic() + 60
        while Path(f'/proc/{pid}').exists() and time.monotonic() < deadline:
            time.sleep(1)
        if Path(f'/proc/{pid}').exists():
            raise RuntimeError('Original kubelet did not exit; no second kubelet was started')

        restart = ['sudo', 'systemd-run', '--unit=' + unit, '--property=Type=exec',
                   '--property=Restart=no', '--expand-environment=no',
                   '--property=WorkingDirectory=' + detail['cwd'],
                   '--property=StandardOutput=append:' + str(out / 'kubelet-restarted.log'),
                   '--property=StandardError=inherit', *argv]
        command('start-recovered-kubelet', restart)
        (run / 'restarted-kubelet-unit.txt').write_text(unit + '\n')
        stop = run / 'stop-restarted-kubelet.sh'
        stop.write_text('#!/usr/bin/env bash\nset -e\nsudo systemctl stop ' + unit + '\n')
        print('Restarted kubelet is managed by', unit, flush=True)
        print('The original startup terminal may warn that its old kubelet PID exited; this is expected.', flush=True)

        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen('http://127.0.0.1:10248/healthz', timeout=2) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(2)
        else:
            raise RuntimeError('Restarted kubelet did not become healthy; inspect kubelet-restarted.log')
        properties = command('recovered-unit', ['systemctl', 'show', unit, '--property=MainPID,ActiveState']).stdout
        check('new-kubelet-process', 'ActiveState=active' in properties
              and f'MainPID={pid}\n' not in properties and 'MainPID=0\n' not in properties, properties)
        kubectl('node-ready-after', ['wait', '--for=condition=Ready', 'node/ubuntu-numa-test', '--timeout=180s'])
        for form, _, cpu, mem in FORMS:
            name = 'recovery-' + form
            kubectl(form + '-ready-after', ['wait', '--for=condition=Ready', 'pod/' + name, '--timeout=180s'])
            after = pod_json(name, 'after-kubelet')
            check(form + '-same-pod-after-kubelet', after['metadata']['uid'] == before[form]['metadata']['uid'])
            masks(form, cpu, mem, 'after-kubelet')
        recovered = snapshots('after-kubelet', root)
        for form, _, _, _ in FORMS:
            uid = before[form]['metadata']['uid']
            for manager in ['cpu', 'memory']:
                old = original[manager].get('entries', {}).get(uid)
                new = recovered[manager].get('entries', {}).get(uid)
                check(form + '-' + manager + '-assignment-recovered', old is not None and old == new,
                      f'original={old}, recovered={new}')

        # Force genuinely new container starts after manager recovery.
        for form, _, cpu, mem in FORMS:
            name = 'recovery-' + form
            pod = pod_json(name, 'before-post-recovery-start')
            old = pod['status']['containerStatuses'][0]
            container_id = old['containerID'].removeprefix('containerd://')
            command(form + '-stop-container', ['sudo', 'ctr', '-n', 'k8s.io', 'tasks', 'kill',
                                               '--signal', 'SIGKILL', container_id])
            deadline = time.monotonic() + 180
            while time.monotonic() < deadline:
                current = json.loads(kubectl(form + '-restart-poll', ['get', 'pod', name, '-o', 'json']).stdout)
                if current['metadata']['uid'] != pod['metadata']['uid']:
                    raise RuntimeError(f'{form}: Pod was replaced during recovery')
                statuses = current.get('status', {}).get('containerStatuses', [])
                if statuses:
                    new = statuses[0]
                    if (new.get('containerID') and new['containerID'] != old['containerID']
                            and new['restartCount'] > old['restartCount'] and new.get('ready')):
                        break
                time.sleep(3)
            else:
                raise RuntimeError(f'{form}: post-recovery container did not restart')
            check(form + '-new-container-after-recovery', True)
            pod_json(name, 'after-post-recovery-start')
            masks(form, cpu, mem, 'post-recovery-start')
        snapshots('after-post-recovery-start', root)
        for form, _, _, _ in FORMS:
            kubectl(form + '-delete', ['delete', 'pod', 'recovery-' + form, '--wait=true', '--timeout=120s'])
    finally:
        (out / 'results.json').write_text(json.dumps(results, indent=2))
        if (run / 'restarted-kubelet-unit.txt').exists():
            command('unit-journal', ['sudo', 'journalctl', '-u', unit, '--no-pager'], check=False)
        print('Recovery checks:', {s: sum(r['result'] == s for r in results) for s in ['PASS', 'FAIL']}, flush=True)
        print('Evidence saved to:', out, flush=True)
    print('PASS: kubelet checkpoint recovery and new container starts for all three placement forms.', flush=True)
    print('Keep the cluster running for the next batch. At final shutdown also run: bash ' + str(stop), flush=True)


if __name__ == '__main__':
    main()
