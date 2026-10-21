#!/usr/bin/env bash
# Build/import the pinned STREAM image and retain the inputs needed to size runs.
set -euo pipefail
umask 077
RUN=$(cat "$HOME/.numa-ticket11-current")
source "$RUN/client-env.sh"
REPO=/home/jukeliu/k8s_workspace/kubernetes
STREAM="$REPO/.scratch/independent-numa-placement/stream"
OUT=$(mktemp -d "$RUN/stream-setup.XXXXXX")
export OUT RUN
echo "STREAM setup evidence: $OUT"
command -v podman >/dev/null || {
  echo 'Install the image builder first: sudo apt-get install -y podman'; exit 1;
}
sudo -v
if ! kubectl --request-timeout=20s get node ubuntu-numa-test -o json \
    > "$OUT/node.json" 2> "$OUT/node.stderr"; then
  cat "$OUT/node.stderr" >&2
  echo "Cannot reach the current cluster API. No image build has started. Evidence: $OUT" >&2
  exit 1
fi

# Node proxy rejects hostnames resolving to loopback. Correct only this guest's
# hostname mapping if that specific observed error still occurs.
if ! kubectl --request-timeout=20s get --raw='/api/v1/nodes/ubuntu-numa-test/proxy/configz' \
    > "$OUT/configz-initial.response" 2> "$OUT/configz-initial.stderr"; then
  if ! grep -q 'address not allowed' "$OUT/configz-initial.stderr"; then
    cat "$OUT/configz-initial.stderr" >&2
    echo "Cannot obtain the runner's kubelet configuration evidence; inspect $OUT"; exit 1
  fi
  sudo python3 - "$OUT" <<'PY'
import ipaddress, json, os, pathlib, socket, sys
out = pathlib.Path(sys.argv[1])
node = json.loads((out / 'node.json').read_text())
addresses = node['status']['addresses']
host = next(a['address'] for a in addresses if a['type'] == 'Hostname')
assert host == 'ubuntu-numa-test' and socket.gethostname().split('.')[0] == host
ips = [a['address'] for a in addresses if a['type'] == 'InternalIP']
ip = next(ipaddress.ip_address(a) for a in ips if ipaddress.ip_address(a).version == 4)
assert not (ip.is_loopback or ip.is_unspecified or ip.is_link_local or ip.is_multicast)
path = pathlib.Path('/etc/hosts')
old = path.read_text()
(out / 'hosts-before.txt').write_text(old)
lines = []
for line in old.splitlines():
    body, mark, comment = line.partition('#')
    fields = body.split()
    if len(fields) > 1 and host in fields[1:]:
        aliases = [a for a in fields[1:] if a != host]
        if aliases:
            lines.append('\t'.join([fields[0], *aliases]) + (' #' + comment if mark else ''))
        elif mark:
            lines.append('#' + comment)
    else:
        lines.append(line)
lines.append(f'{ip}\t{host}')
new = '\n'.join(lines) + '\n'
path.write_text(new)
(out / 'hosts-after.txt').write_text(new)
owner = out.stat()
for name in ['hosts-before.txt', 'hosts-after.txt']:
    os.chown(out / name, owner.st_uid, owner.st_gid)
print(f'Updated guest hostname mapping: {host} -> {ip}', flush=True)
PY
fi
kubectl --request-timeout=20s get --raw='/api/v1/nodes/ubuntu-numa-test/proxy/configz' > "$OUT/configz.json"

echo 'Building the pinned GCC/OpenMP/Python STREAM image...'
sudo podman version > "$OUT/podman-version.txt"
sudo podman build --platform linux/arm64 --tag localhost/numa-stream:ticket11 \
  "$STREAM" 2>&1 | tee "$OUT/build.log"
sudo podman image inspect localhost/numa-stream:ticket11 > "$OUT/image-inspect.json"
sudo podman save --format oci-archive --output "$OUT/stream-image.tar" \
  localhost/numa-stream:ticket11
sudo chown "$(id -u):$(id -g)" "$OUT/stream-image.tar"
sudo ctr -n k8s.io images import --local --platform linux/arm64 \
  --base-name localhost/numa-stream --digests --label io.cri-containerd.image=managed \
  "$OUT/stream-image.tar" 2>&1 | tee "$OUT/import.log"
sudo ctr -n k8s.io images ls > "$OUT/containerd-images.txt"

python3 - <<'PY'
import hashlib, json, os, pathlib, re, tarfile
out = pathlib.Path(os.environ['OUT'])
run = pathlib.Path(os.environ['RUN'])
with tarfile.open(out / 'stream-image.tar') as archive:
    member = archive.extractfile('index.json')
    assert member is not None, 'OCI archive has no index.json'
    index = json.load(member)
(out / 'oci-index.json').write_text(json.dumps(index, indent=2))
assert len(index['manifests']) == 1, 'Expected one ARM64 image descriptor'
digest = index['manifests'][0]['digest']
assert re.fullmatch(r'sha256:[0-9a-f]{64}', digest)
image = 'localhost/numa-stream@' + digest
assert any(line.split()[0] == image for line in (out / 'containerd-images.txt').read_text().splitlines() if line.split()), \
    'Imported digest reference is missing; inspect import.log and containerd-images.txt'
(out / 'image-reference.txt').write_text(image + '\n')
with (out / 'stream-image.tar').open('rb') as file:
    archive_hash = hashlib.file_digest(file, 'sha256').hexdigest()
(out / 'archive-sha256.txt').write_text(archive_hash + '\n')

cpu_info, caches, missing = {}, {}, []
for cpu in [1, 2, 3]:
    entries = []
    for directory in pathlib.Path(f'/sys/devices/system/cpu/cpu{cpu}/cache').glob('index*'):
        entries.append({key: (directory / key).read_text().strip()
                        for key in ['level', 'type', 'size', 'shared_cpu_list']})
    cpu_info[str(cpu)] = entries
    usable = [e for e in entries if e['type'] in ['Unified', 'Data']]
    if not usable:
        missing.append(cpu)
        continue
    last = max(int(e['level']) for e in usable)
    for entry in usable:
        if int(entry['level']) == last:
            size = entry['size']
            amount = int(size[:-1]) * {'K':1024, 'M':1048576, 'G':1073741824}[size[-1]]
            caches[entry['shared_cpu_list']] = amount
nodes = {}
for directory in pathlib.Path('/sys/devices/system/node').glob('node[0-9]*'):
    nodes[directory.name] = {key: (directory / key).read_text()
                             for key in ['cpulist', 'distance', 'meminfo']}
settings = {}
for name in ['/sys/kernel/mm/transparent_hugepage/enabled',
             '/sys/kernel/mm/transparent_hugepage/defrag',
             '/proc/sys/kernel/numa_balancing', '/proc/meminfo', '/proc/loadavg']:
    path = pathlib.Path(name)
    settings[name] = path.read_text() if path.exists() else 'unavailable'
metadata = {'image': image, 'cpu_node': 0, 'eligible_cpus': [1, 2, 3],
            'memory_nodes': [0, 1], 'cpu_cache_entries': cpu_info,
            'guest_llc_bytes': sum(caches.values()) if not missing else None,
            'guest_llc_caches': caches, 'cpus_missing_cache_metadata': missing,
            'nodes': nodes, 'settings': settings,
            'boundary': 'QEMU ARM64/HVF guest on Apple host; virtual NUMA; physical memory-type mapping unverified'}
(out / 'vm-metadata.json').write_text(json.dumps(metadata, indent=2))
(run / 'stream-setup-path.txt').write_text(str(out) + '\n')
print('\nSTREAM image ready:', image)
print('Guest LLC bytes:', metadata['guest_llc_bytes'] if not missing else 'UNAVAILABLE in guest sysfs')
print('Kubelet configz: captured')
print('Setup evidence:', out)
PY
