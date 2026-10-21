#!/usr/bin/env bash
# Readiness capture only: no test Pods are created.
set -euo pipefail
RUN=$(cat "$HOME/.numa-ticket11-current")
source "$RUN/client-env.sh"
OUT=$(mktemp -d "$RUN/readiness.XXXXXX")
echo "Saving checks to $OUT"
kubectl get --raw='/readyz?verbose' > "$OUT/apiserver-readyz.txt"
kubectl wait --for=condition=Ready node/ubuntu-numa-test --timeout=180s
kubectl get node ubuntu-numa-test -o json > "$OUT/node.json"
kubectl get nodes -o wide | tee "$OUT/nodes.txt"
kubectl get pods -A -o wide > "$OUT/pods.txt"
if ! kubectl get --raw='/api/v1/nodes/ubuntu-numa-test/proxy/configz' \
    > "$OUT/configz.response" 2> "$OUT/configz-error.txt"; then
  cat "$OUT/configz-error.txt" >&2
  python3 - "$OUT/node.json" <<'PY'
import json, sys
node = json.load(open(sys.argv[1]))
print('Reported Node addresses:', node['status'].get('addresses', []))
PY
  getent ahostsv4 ubuntu-numa-test > "$OUT/hostname-resolution.txt" || true
  echo "Configz capture failed. Node diagnostics are in $OUT; configz.json was not created." >&2
  exit 1
fi
python3 -m json.tool "$OUT/configz.response" > "$OUT/configz.json"
kubectl explain pod.spec.cpuNUMA > "$OUT/api-cpuNUMA.txt"
kubectl explain pod.spec.memNUMA > "$OUT/api-memNUMA.txt"
python3 - "$OUT/configz.json" <<'PY'
import json, sys
c = json.load(open(sys.argv[1]))['kubeletconfig']
expected = {
    'cpuManagerPolicy': 'static', 'memoryManagerPolicy': 'Static',
    'topologyManagerPolicy': 'independent-numa', 'topologyManagerScope': 'pod',
    'reservedSystemCPUs': '0,4', 'cgroupDriver': 'systemd', 'failSwapOn': True,
}
for key, value in expected.items():
    print(f'{key}: {c.get(key)}')
    assert c.get(key) == value, f'Expected {key}={value!r}'
print('reservedMemory:', c.get('reservedMemory'))
print('Configuration checks passed; workload placement has not yet been tested.')
PY
printf '\nReadiness evidence: %s\n' "$OUT"
