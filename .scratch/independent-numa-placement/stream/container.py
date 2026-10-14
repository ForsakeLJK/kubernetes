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


"""Launch STREAM with Docker/Podman and retain runtime exit/OOM/image evidence."""

import argparse
import json
from pathlib import Path
import subprocess
import time
from typing import Any

from run import save


def inspect(engine: str, command: list[str], artifact: Path) -> dict[str, Any]:
    result = subprocess.run([engine] + command, capture_output=True, text=True)
    artifact.write_text(result.stdout)
    artifact.with_suffix('.stderr.txt').write_text(result.stderr)
    result.check_returncode()
    data = json.loads(result.stdout)
    return data[0]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--engine', default='podman')
    parser.add_argument('--image', required=True, help='locally built image name or digest')
    parser.add_argument('--output', required=True, type=Path, help='new host evidence directory')
    parser.add_argument('--memory', default='1g', help='container memory limit including runtime compilation')
    parser.add_argument('--cpuset-cpus', help='optional CPU allocation; never a memory-binding override')
    parser.add_argument('settings', nargs=argparse.REMAINDER, help='run.py flags after --')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    meta: dict[str, Any] = {'schema_version': 1, 'status': 'running', 'started_unix': time.time(),
                            'requested_image': args.image, 'memory_limit': args.memory,
                            'cpuset_cpus': args.cpuset_cpus}
    save(output / 'launch.json', meta)
    (output / 'runtime.stdout.txt').touch()
    (output / 'runtime.stderr.txt').touch()
    code = 1
    try:
        image = inspect(args.engine, ['image', 'inspect', args.image], output / 'image-inspect.json')
        image_id = image.get('Id') or image.get('ID')
        if not image_id:
            raise ValueError('image inspect returned no immutable image ID; build the image first')
        meta['image_id'] = image_id
        settings = args.settings[1:] if args.settings[:1] == ['--'] else args.settings
        if any(flag.split('=')[0] in ('--output', '--image-identity') for flag in settings):
            raise ValueError('the launcher supplies --output and --image-identity')
        command = [args.engine, 'run', '--cidfile', str(output / 'container.id'),
                   '--memory', args.memory, '--volume', f'{output}:/evidence']
        if args.cpuset_cpus:
            command += ['--cpuset-cpus', args.cpuset_cpus]
        command += [image_id, '--output', '/evidence/run', '--image-identity', image_id] + settings
        meta['command'] = command
        save(output / 'launch.json', meta)
        with (output / 'runtime.stdout.txt').open('wb') as stdout, (output / 'runtime.stderr.txt').open('wb') as stderr:
            result = subprocess.run(command, stdout=stdout, stderr=stderr)
        meta['returncode'] = result.returncode
        if not (output / 'container.id').exists():
            raise ValueError('runtime created no container; inspect runtime.stderr.txt')
        container_id = (output / 'container.id').read_text().strip()
        meta['container_id'] = container_id
        container = inspect(args.engine, ['inspect', container_id], output / 'container-inspect.json')
        state = container['State']
        meta['container_exit_code'] = state.get('ExitCode')
        meta['oom_killed'] = state.get('OOMKilled')
        inner_path = output / 'run/run.json'
        inner = json.loads(inner_path.read_text()) if inner_path.exists() else {}
        if state.get('OOMKilled'):
            meta['status'] = 'oom'
        elif result.returncode != 0 or state.get('ExitCode') != 0:
            meta['status'] = 'process_failed'
        elif inner.get('status') != 'valid':
            meta['status'] = 'invalid_or_missing_workload_result'
        else:
            meta['status'] = 'valid'
            code = 0
        meta['workload_status'] = inner.get('status', 'missing')
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        meta.update(status='launch_failed', error=str(error))
    meta['finished_unix'] = time.time()
    save(output / 'launch.json', meta)
    print(json.dumps({'status': meta['status'], 'output': str(output)}))
    return code


if __name__ == '__main__':
    raise SystemExit(main())
