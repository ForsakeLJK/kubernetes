#!/usr/bin/env bash
# Exercise masks across a gated init, restartable sidecar and two applications.
set -euo pipefail
RUN=$(cat "$HOME/.numa-ticket11-current")
source "$RUN/client-env.sh"
CHECKER=/home/jukeliu/k8s_workspace/kubernetes/.scratch/explicit-numa-placement/runtime/check-masks.py
OUT=$(mktemp -d "$RUN/pod-wide.XXXXXX")
echo "Evidence directory: $OUT"

# Free the three-CPU groups needed by the fixtures; preserve the prior records.
for pod in split-test cpu-shortage memory-only-test; do
  kubectl -n numa-validation get pod "$pod" --ignore-not-found -o json \
    > "$OUT/prior-$pod.json"
done
kubectl -n numa-validation delete pod split-test cpu-shortage memory-only-test \
  --ignore-not-found --wait=true --timeout=120s

python3 - "$OUT" <<'PY'
import json, pathlib, sys
out = pathlib.Path(sys.argv[1])
def container(name, command=None):
    return {
        'name': name, 'image': 'busybox:1.36',
        'command': ['sh', '-c', command or 'sleep 86400'],
        'resources': {
            'requests': {'cpu': '1', 'memory': '128Mi'},
            'limits': {'cpu': '1', 'memory': '128Mi'},
        },
    }
for form, fields in [
    ('explicit', {'cpuNUMA': 0, 'memNUMA': 1}),
    ('cpu-only', {'cpuNUMA': 0}),
    ('memory-only', {'memNUMA': 1}),
]:
    sidecar = container('sidecar')
    sidecar['restartPolicy'] = 'Always'
    pod = {
        'apiVersion': 'v1', 'kind': 'Pod',
        'metadata': {'name': f'pod-wide-{form}', 'namespace': 'numa-validation'},
        'spec': {
            'nodeName': 'ubuntu-numa-test', **fields,
            'initContainers': [sidecar, container('init',
                'while [ ! -f /tmp/numa-release ]; do sleep 1; done')],
            'containers': [container('app-a'), container('app-b')],
        },
    }
    (out / f'{form}.json').write_text(json.dumps(pod, indent=2))
PY

for form in explicit cpu-only memory-only; do
  POD="pod-wide-$form"
  case "$form" in
    explicit) C=0; M=1; CPUS=0-3 ;;
    cpu-only) C=0; M=0; CPUS=0-3 ;;
    memory-only) C=1; M=1; CPUS=4-7 ;;
  esac
  mkdir "$OUT/$form"
  kubectl create -f "$OUT/$form.json"

  # Pod Ready cannot occur until we explicitly release the init container.
  python3 - "$POD" "$OUT/$form/init-pod.json" <<'PY'
import json, pathlib, subprocess, sys, time
name, output = sys.argv[1:]
deadline = time.monotonic() + 180
while time.monotonic() < deadline:
    pod = json.loads(subprocess.check_output([
        'kubectl', '-n', 'numa-validation', 'get', 'pod', name, '-o', 'json']))
    pathlib.Path(output).write_text(json.dumps(pod, indent=2))
    if pod.get('status', {}).get('phase') == 'Failed':
        raise SystemExit(f'{name}: {pod["status"]}')
    statuses = pod.get('status', {}).get('initContainerStatuses', [])
    if any(c['name'] == 'init' and 'running' in c.get('state', {}) for c in statuses):
        break
    time.sleep(2)
else:
    raise SystemExit(f'{name}: init did not start; inspect the Pod and {output}')
PY

  for container in init sidecar; do
    python3 "$CHECKER" \
      --namespace numa-validation --pod "$POD" --container "$container" \
      --cpu-node "$C" --memory-node "$M" --node-cpus "$CPUS" \
      --output "$OUT/$form/init-phase-$container-masks.json" \
      > "$OUT/$form/init-phase-$container-output.txt"
    echo "PASS $form: $container during init (CPU node $C, memory node $M)"
  done

  kubectl -n numa-validation exec "$POD" -c init -- touch /tmp/numa-release
  kubectl -n numa-validation wait --for=condition=Ready "pod/$POD" --timeout=180s
  for container in app-a app-b sidecar; do
    python3 "$CHECKER" \
      --namespace numa-validation --pod "$POD" --container "$container" \
      --cpu-node "$C" --memory-node "$M" --node-cpus "$CPUS" \
      --output "$OUT/$form/app-phase-$container-masks.json" \
      > "$OUT/$form/app-phase-$container-output.txt"
    echo "PASS $form: $container during application phase (CPU node $C, memory node $M)"
  done
  kubectl -n numa-validation get pod "$POD" -o json > "$OUT/$form/app-pod.json"
  kubectl -n numa-validation delete pod "$POD" --wait=true --timeout=120s
done

echo 'PASS: pod-wide masks for explicit, CPU-only and memory-only forms.'
echo "Evidence saved to: $OUT"
