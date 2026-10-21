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

"""Synthetic evidence at ticket 10's public preparation/run/report CLI seams."""

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def config():
    return {"node": "worker", "namespace": "numa-validation", "cpu_node": 0,
            "memory_nodes": [0, 3], "image": "example/stream@sha256:" + "a" * 64,
            "cpu": 2, "memory": "128Mi", "target_available_bytes": [134217728] * 2,
            "overhead_bytes": 67108864, "llc_bytes": 1048576, "array_size": 1000000,
            "threads": [1, 2], "iterations": 10, "measured_runs": 5,
            "observe_seconds": 0, "residency_samples": 2, "timeout": 60,
            "mode": "bandwidth", "cflags": [], "evidence_origin": "synthetic",
            "environment": {"kind": "vm", "description": "synthetic guest fixture",
                            "interference": "fixture; no live workload",
                            "settings": "fixture THP/ballooning/frequency settings",
                            "fork_commit": "fixture-commit", "files": []}}


class EvaluationCLI(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.output = self.base / "evaluation"

    def cli(self, *args):
        return subprocess.run([sys.executable, str(ROOT / "paired.py"), *map(str, args)],
                              capture_output=True, text=True)

    def prepare(self, settings=None):
        path = self.base / "config.json"
        path.write_text(json.dumps(settings or config()))
        return self.cli("prepare", "--config", path, "--output", self.output)

    def evidence(self, settings=None):
        """Write explicitly synthetic full execution records; rates are fixture literals."""
        self.assertEqual(self.prepare(settings).returncode, 0)
        plan = json.loads((self.output / "plan.json").read_text())
        source = json.loads((ROOT.parent / "stream/source.json").read_text())
        raw = (ROOT.parent / "stream/tests/fixtures/valid.stdout").read_text()
        for row in plan["executions"]:
            directory = self.output / "executions" / row["id"]
            run = directory / "workload/run"
            run.mkdir(parents=True)
            memory = 0 if row["case"] == "a" else 3
            status = f"Cpus_allowed_list:\t4-5\nMems_allowed_list:\t{memory}\n"
            (run.parent / "effective-status.txt").write_text(status)
            (run.parent / "node-cpus.txt").write_text("4-7\n")
            (run.parent / "host.json").write_text(json.dumps({"llc_bytes":1048576, "kernel":"fixture"}))
            (run / "proc-status.txt").write_text(f"Cpus_allowed_list:\t4\nMems_allowed_list:\t{memory}\n")
            (run / "stdout.txt").write_text(raw.replace("Number of Threads requested = 2", f"Number of Threads requested = {row['threads']}").replace("Number of Threads counted = 2", f"Number of Threads counted = {row['threads']}") + "\n(= 100 clock ticks)\n")
            (run / "stderr.txt").write_text("synthetic fixture, not a measurement\n")
            (run / "stream").write_bytes(b"synthetic executable fixture")
            (run / "stream-observed.c").write_text("/* synthetic source fixture */\n")
            arrays = [{"name":name, "start":hex(0x1000000*(i+1)),
                       "end":hex(0x1000000*(i+1)+8000000), "bytes":8000000}
                      for i, name in enumerate("abc")]
            identity = {"pid":123, "phase":"initialized_before_calibration", "arrays":arrays}
            residency = {"method":"move_pages query only", "status":"sampled", "page_size":4096,
                         "arrays":[{"name":name, "total_pages":1954, "sampled_pages":2,
                                    "errno":0, "page_status":[memory, memory]} for name in "abc"]}
            build = {"source":source, "compiler":"fixture GCC", "command":["gcc", "-O3", "-fopenmp"],
                     "base_image":"fixture base", "image_identity":config()["image"],
                     "harness_sha256":{name:hashlib.sha256((ROOT.parent/"stream"/name).read_bytes()).hexdigest()
                                       for name in ["run.py", "parse.py", "observe.c", "observe.h"]},
                     "instrumented_source_sha256":hashlib.sha256((run/"stream-observed.c").read_bytes()).hexdigest(),
                     "binary_sha256":hashlib.sha256((run/"stream").read_bytes()).hexdigest()}
            settings = {key:plan["config"][key] for key in ["array_size", "iterations", "observe_seconds",
                        "residency_samples", "timeout", "mode", "llc_bytes"]}
            settings.update(threads=row["threads"], cflag=[], serial=False)
            meta = {"status":"valid", "returncode":0, "pid":123, "config":settings, "build":build,
                    "allocated_cpus":[4, 5], "oom":"not_observed", "array_identity_status":"verified",
                    "affinity_status":"verified", "observed_threads":row["threads"],
                    "environment":{"OMP_NUM_THREADS":str(row["threads"]), "OMP_DYNAMIC":"FALSE",
                                   "OMP_PROC_BIND":"close", "OMP_PLACES":"{4},{5}",
                                   "STREAM_OBSERVE_SECONDS":"0", "STREAM_RESIDENCY_SAMPLES":"2",
                                   "STREAM_RUN_DIR":"/evidence/run"}}
            threads = {"threads":[{"thread":i, "cpu":4+i, "allowed_cpus":[4+i]}
                                  for i in range(row["threads"])]}
            for name, data in [("run.json", meta), ("arrays.json", identity),
                               ("residency.json", residency), ("threads.json", threads)]:
                (run / name).write_text(json.dumps(data))
            (run.parent / "execution.json").write_text('{"returncode":0}')
            (directory / "orchestration.json").write_text('{"status":"completed","evidence_origin":"synthetic"}')
            pod = json.loads((self.output / row["manifest"]).read_text())
            pod["status"] = {"phase":"Running", "qosClass":"Guaranteed", "containerStatuses":[{
                "name":"stream", "imageID":"containerd://sha256:"+"e"*64, "restartCount":0,
                "state":{"running":{}}, "lastState":{}}]}
            (directory / "pod").mkdir()
            (directory / "pod/stdout.txt").write_text(json.dumps(pod))
        return plan

    def test_collector_continues_when_guest_cache_size_is_missing(self):
        import ast
        tree = ast.parse((ROOT / 'paired.py').read_text())
        collector = next(ast.literal_eval(n.value) for n in tree.body
                         if isinstance(n, ast.Assign)
                         and any(isinstance(t, ast.Name) and t.id == 'COLLECTOR' for t in n.targets))
        evidence = self.base / 'collector-evidence'
        evidence.mkdir()
        cache = self.base / 'sys/cpu/cpu1/cache/index2'
        cache.mkdir(parents=True)
        (cache / 'level').write_text('2')
        (cache / 'type').write_text('Unified')
        (cache / 'shared_cpu_list').write_text('0-7')
        node = self.base / 'sys/node/node0'
        node.mkdir(parents=True)
        (node / 'cpulist').write_text('0-3')
        proc = self.base / 'proc-status'
        proc.write_text('Cpus_allowed_list: 1\nMems_allowed_list: 0\n')
        collector = collector.replace("pathlib.Path('/evidence')", 'pathlib.Path(' + repr(str(evidence)) + ')')
        collector = collector.replace('/sys/devices/system', str(self.base / 'sys'))
        collector = collector.replace('/proc/self/status', str(proc))
        collector = collector.replace("__import__('os').sched_getaffinity(0)", '{1}')
        collector = collector.replace('while True: time.sleep(60)', '')
        result = subprocess.run([sys.executable, '-c', collector,
                                 json.dumps({'cpu_node':0, 'argv':[sys.executable, '-c', 'pass']})],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        host = json.loads((evidence / 'host.json').read_text())
        self.assertIsNone(host['llc_bytes'])
        self.assertTrue(host['cache_errors'])
        self.assertEqual(json.loads((evidence / 'execution.json').read_text())['returncode'], 0)

    def test_unknown_cache_smoke_preserves_unknown_without_bandwidth_claim(self):
        c = config()
        c.update(mode='smoke', llc_bytes=None)
        plan = self.evidence(c)
        for row in plan['executions']:
            host = self.output / 'executions' / row['id'] / 'workload/host.json'
            host.write_text(json.dumps({'llc_bytes':None, 'cache_errors':[
                {'cpu':4, 'error':'cache size unavailable'}]}))
            pod = json.loads((self.output / row['manifest']).read_text())
            collector_config = json.loads(pod['spec']['containers'][0]['command'][-1])
            self.assertNotIn('--llc-bytes', collector_config['argv'])
        result = self.cli('report', '--output', self.output)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        report = json.loads((self.output / 'report.json').read_text())
        self.assertEqual(report['status'], 'PASS_FIXTURE')
        self.assertFalse(report['successful_controlled_comparison'])

    def test_unknown_cache_rejected_for_bandwidth(self):
        c = config()
        c['llc_bytes'] = None
        self.assertNotEqual(self.prepare(c).returncode, 0)

    def test_missing_observed_cache_rejected_for_bandwidth(self):
        plan = self.evidence()
        host = self.output / 'executions' / plan['executions'][0]['id'] / 'workload/host.json'
        host.write_text(json.dumps({'llc_bytes':None, 'cache_errors':['missing size']}))
        self.assertNotEqual(self.cli('report', '--output', self.output).returncode, 0)

    def test_report_keeps_fixture_rates_and_never_claims_live_comparison(self):
        self.evidence()
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["status"], "PASS_FIXTURE")
        self.assertFalse(report["successful_controlled_comparison"])
        self.assertEqual(len(report["summaries"]), 4)
        self.assertTrue(all(group["count"] == 5 for group in report["summaries"]))
        self.assertEqual(report["summaries"][0]["kernels"]["Copy"],
                         {"median":12000.0, "min":12000.0, "max":12000.0, "unit":"MB/s"})

    def test_incomplete_plan_reports_not_run_without_fabricated_throughput(self):
        self.assertEqual(self.prepare().returncode, 0)
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["status"], "NOT RUN")
        self.assertEqual(len(report["executions"]), 24)
        self.assertTrue(all(s["kernels"] == {} for s in report["summaries"]))

    def test_excludes_warmups_and_reports_known_median_and_range(self):
        plan = self.evidence()
        rows = [r for r in plan["executions"] if r["threads"] == 1 and r["case"] == "a"]
        for row, rate in zip(rows, [999999, 10000, 20000, 30000, 40000, 50000]):
            path = self.output / "executions" / row["id"] / "workload/run/stdout.txt"
            path.write_text(path.read_text().replace("12000.0", str(rate)))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["summaries"][0]["kernels"]["Copy"],
                         {"median":30000.0, "min":10000.0, "max":50000.0, "unit":"MB/s"})

    def test_rejects_bad_sizing_undersampling_oversubscription_and_short_runs(self):
        for field, value in [("array_size", 999999), ("llc_bytes", 33554432),
                             ("target_available_bytes", [10000000, 134217728]),
                             ("memory", "16Mi"), ("threads", [1, 3]), ("threads", [2]),
                             ("measured_runs", 4), ("residency_samples", 0)]:
            with self.subTest(field=field, value=value):
                settings = config()
                settings[field] = value
                result = self.prepare(settings)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertFalse(self.output.exists())

    def test_wrong_placement_bad_numerics_missing_residency_and_build_mismatch_prevent_success(self):
        plan = self.evidence()
        directory = self.output / "executions" / plan["executions"][2]["id"]
        changes = [("workload/effective-status.txt", "Cpus_allowed_list:\t6-7\nMems_allowed_list:\t3\n", "CPU"),
                   ("workload/run/stdout.txt", "Failed Validation\n", "validation"),
                   ("workload/run/residency.json", '{"status":"unavailable"}', "residency"),
                   ("orchestration.json", '{"status":"failed","error":"OOMKilled"}', "OOMKilled")]
        for file, text, reason in changes:
            with self.subTest(file=file):
                path = directory / file
                previous = path.read_text()
                path.write_text(text)
                result = self.cli("report", "--output", self.output)
                path.write_text(previous)
                self.assertEqual(result.returncode, 1, result.stderr)
                report = json.loads((self.output / "report.json").read_text())
                self.assertFalse(report["successful_controlled_comparison"])
                row = report["executions"][2]
                self.assertEqual(row["status"], "FAIL")
                self.assertIn(reason, " ".join(row["reasons"]))

    def test_same_cpu_node_with_changed_actual_set_is_uncontrolled(self):
        plan = self.evidence()
        directory = self.output / "executions" / plan["executions"][2]["id"] / "workload"
        (directory / "effective-status.txt").write_text("Cpus_allowed_list:\t6-7\nMems_allowed_list:\t3\n")
        (directory / "run/proc-status.txt").write_text("Cpus_allowed_list:\t6\nMems_allowed_list:\t3\n")
        path = directory / "run/run.json"
        meta = json.loads(path.read_text())
        meta["allocated_cpus"] = [6, 7]
        meta["environment"]["OMP_PLACES"] = "{6},{7}"
        path.write_text(json.dumps(meta))
        (directory / "run/threads.json").write_text('{"threads":[{"thread":0,"cpu":6,"allowed_cpus":[6]}]}')
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertIn("cpu_set mismatch", " ".join(report["executions"][2]["reasons"]))

    def test_recorded_binary_mismatch_is_uncontrolled(self):
        plan = self.evidence()
        path = self.output / "executions" / plan["executions"][2]["id"] / "workload/run/run.json"
        meta = json.loads(path.read_text())
        meta["build"]["binary_sha256"] = "f"*64
        path.write_text(json.dumps(meta))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertIn("binary hash", " ".join(report["executions"][2]["reasons"]))

    def test_different_retained_binaries_with_honest_metadata_are_uncontrolled(self):
        plan = self.evidence()
        run = self.output / "executions" / plan["executions"][2]["id"] / "workload/run"
        (run / "stream").write_bytes(b"different synthetic binary")
        meta = json.loads((run / "run.json").read_text())
        meta["build"]["binary_sha256"] = hashlib.sha256((run / "stream").read_bytes()).hexdigest()
        (run / "run.json").write_text(json.dumps(meta))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertIn("build mismatch", " ".join(report["executions"][2]["reasons"]))

    def test_malformed_records_or_wrong_sample_nodes_cannot_pass(self):
        plan = self.evidence()
        run = self.output / "executions" / plan["executions"][2]["id"] / "workload/run"
        path = run / "residency.json"
        previous = path.read_text()
        for data in [[], None, {"status":"sampled", "method":"move_pages query only", "page_size":4096,
                               "arrays":[{"name":name, "total_pages":1954, "sampled_pages":2,
                                          "errno":0, "page_status":[3, 0]} for name in "abc"]}]:
            with self.subTest(data=data):
                path.write_text(json.dumps(data))
                result = self.cli("report", "--output", self.output)
                self.assertEqual(result.returncode, 1, result.stderr)
                report = json.loads((self.output / "report.json").read_text())
                self.assertFalse(report["successful_controlled_comparison"])
        path.write_text(previous)

    def test_hardware_labels_require_retained_node_mapping_and_operator_verification(self):
        settings = config()
        settings["evidence_origin"] = "live"
        settings["environment"].update(kind="physical", backing=[
            {"type":"DDR", "node":0, "evidence":"mapping.txt", "verified_by":"fixture operator"},
            {"type":"HBM", "node":3, "evidence":"mapping.txt", "verified_by":"fixture operator"}], files=["mapping.txt"])
        (self.base / "mapping.txt").write_text("Synthetic mapping proof, not actual hardware evidence\n")
        self.assertEqual(self.prepare(settings).returncode, 0)
        self.cli("report", "--output", self.output)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["classification"], "verified_ddr_hbm")
        self.assertFalse(report["successful_controlled_comparison"])
        proof = next((self.output / "environment").iterdir())
        proof.write_text("")
        self.cli("report", "--output", self.output)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["classification"], "hardware_numa_measurement")
        plan = json.loads((self.output / "plan.json").read_text())
        plan["config"]["environment"]["kind"] = "vm"
        (self.output / "plan.json").write_text(json.dumps(plan))
        self.cli("report", "--output", self.output)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["classification"], "vm_measurement")

    def test_failed_cluster_launch_retains_failure_and_stops_the_sweep(self):
        settings = config()
        settings["evidence_origin"] = "live"
        self.assertEqual(self.prepare(settings).returncode, 0)
        engine = self.base / "kubectl-fixture"
        engine.write_text("#!"+sys.executable+"\nimport json,sys\na=sys.argv[1:]\n"+r'''
if a[:2] == ['get','--raw']:
    print(json.dumps({'kubeletconfig':{'cpuManagerPolicy':'static','memoryManagerPolicy':'Static',
        'topologyManagerPolicy':'independent-numa','topologyManagerScope':'pod'}}))
elif a[:2] == ['get','node']:
    print(json.dumps({'metadata':{'name':'worker'},'status':{'nodeInfo':{'operatingSystem':'linux'}}}))
else:
    print('synthetic API refusal', file=sys.stderr)
    sys.exit(1)
''')
        engine.chmod(0o755)
        result = self.cli("run", "--output", self.output, "--kubectl", engine)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(len(list((self.output / "executions").iterdir())), 1)
        directory = next((self.output / "executions").iterdir())
        self.assertIn("API refusal", (directory / "create/stderr.txt").read_text())
        self.assertFalse((directory / "delete").exists())

    def test_api_canonical_memory_quantities_compare_equal_but_wrong_values_fail(self):
        settings = config()
        settings["memory"] = "1024Mi"
        plan = self.evidence(settings)
        for row in plan["executions"]:
            path = self.output / "executions" / row["id"] / "pod/stdout.txt"
            pod = json.loads(path.read_text())
            for kind in ["requests", "limits"]:
                pod["spec"]["containers"][0]["resources"][kind]["memory"] = "1Gi"
            path.write_text(json.dumps(pod))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 0, result.stderr)
        for kind in ["requests", "limits"]:
            pod["spec"]["containers"][0]["resources"][kind]["memory"] = "2Gi"
        path.write_text(json.dumps(pod))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 1, result.stderr)
        report = json.loads((self.output / "report.json").read_text())
        self.assertIn("resources", " ".join(report["executions"][-1]["reasons"]))

    def test_api_canonical_decimal_cpu_quantities_compare_equal_but_wrong_values_fail(self):
        settings = config()
        settings["cpu"] = 1000
        plan = self.evidence(settings)
        for row in plan["executions"]:
            directory = self.output / "executions" / row["id"]
            path = directory / "pod/stdout.txt"
            pod = json.loads(path.read_text())
            for kind in ["requests", "limits"]:
                pod["spec"]["containers"][0]["resources"][kind]["cpu"] = "1k"
            path.write_text(json.dumps(pod))
            wrapper = directory / "workload"
            memory = 0 if row["case"] == "a" else 3
            (wrapper / "effective-status.txt").write_text(f"Cpus_allowed_list:\t4-1003\nMems_allowed_list:\t{memory}\n")
            (wrapper / "node-cpus.txt").write_text("4-1003\n")
            meta = json.loads((wrapper / "run/run.json").read_text())
            meta["allocated_cpus"] = list(range(4, 1004))
            meta["environment"]["OMP_PLACES"] = ",".join("{%d}" % cpu for cpu in range(4, 1004))
            (wrapper / "run/run.json").write_text(json.dumps(meta))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 0, result.stderr)
        for kind in ["requests", "limits"]:
            pod["spec"]["containers"][0]["resources"][kind]["cpu"] = "2k"
        path.write_text(json.dumps(pod))
        result = self.cli("report", "--output", self.output)
        self.assertEqual(result.returncode, 1, result.stderr)

    def test_lost_create_response_cleans_owned_pod_and_preserves_name_collisions(self):
        settings = config()
        settings["evidence_origin"] = "live"
        for collision in (False, True):
            with self.subTest(collision=collision):
                self.output = self.base / ("collision" if collision else "owned")
                self.assertEqual(self.prepare(settings).returncode, 0)
                active = self.base / ("collision-active" if collision else "owned-active")
                engine = self.base / "kubectl-fixture"
                engine.write_text("#!"+sys.executable+"\nimport json,pathlib,sys\nactive=pathlib.Path("+repr(str(active))+")\ncollision="+repr(collision)+"\n"+r'''
a=sys.argv[1:]
if a[:2] == ['get','--raw']:
    print(json.dumps({'kubeletconfig':{'cpuManagerPolicy':'static','memoryManagerPolicy':'Static',
        'topologyManagerPolicy':'independent-numa','topologyManagerScope':'pod'}}))
elif a[:2] == ['get','node']:
    print(json.dumps({'metadata':{'name':'worker'},'status':{'nodeInfo':{'operatingSystem':'linux'}}}))
else:
    a=a[2:]
    if a[0] == 'create':
        pod=json.loads(pathlib.Path(a[2]).read_text())
        if collision: pod['metadata']['labels']={'numa-stream-owner':'different-experiment'}
        active.write_text(json.dumps(pod))
        print('synthetic lost create response', file=sys.stderr)
        sys.exit(1)
    elif a[:2] == ['get','pod']: print(active.read_text())
    elif a[0] == 'delete': active.unlink()
    else: print('{}')
''')
                engine.chmod(0o755)
                result = self.cli("run", "--output", self.output, "--kubectl", engine)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertEqual(active.exists(), collision)
                directory = next((self.output / "executions").iterdir())
                self.assertEqual((directory / "delete/command.json").exists(), not collision)

    def test_orchestrates_external_kubectl_boundary_and_cleans_each_pod(self):
        settings = config()
        settings["mode"] = "smoke"
        settings["threads"] = [1]
        plan = self.evidence(settings)
        fixtures = self.base / "cluster-fixtures"
        fixtures.mkdir()
        for row in plan["executions"]:
            directory = self.output / "executions" / row["id"]
            (fixtures / (row["pod"]+".json")).write_bytes((directory/"pod/stdout.txt").read_bytes())
            with tarfile.open(fixtures / (row["pod"]+".tar"), "w") as tar:
                tar.add(directory/"workload", arcname=".")
        __import__('shutil').rmtree(self.output / "executions")
        plan["config"]["evidence_origin"] = "live"
        (self.output / "plan.json").write_text(json.dumps(plan))
        engine = self.base / "kubectl-fixture"
        engine.write_text("#!"+sys.executable+"\nimport json,pathlib,sys\np=pathlib.Path("+repr(str(fixtures))+")\n"+r'''
a = sys.argv[1:]
with (p/'calls.jsonl').open('a') as f: f.write(json.dumps(a)+'\n')
if a[:2] == ['get','--raw']:
    print(json.dumps({'kubeletconfig':{'cpuManagerPolicy':'static','memoryManagerPolicy':'Static',
        'topologyManagerPolicy':'independent-numa','topologyManagerScope':'pod'}}))
elif a[:2] == ['get','node']:
    print(json.dumps({'metadata':{'name':'worker'},'status':{'nodeInfo':{'operatingSystem':'linux'}}}))
else:
    a = a[2:]
    if a[0] == 'create':
        pod=json.loads(pathlib.Path(a[2]).read_text())['metadata']['name']
        (p/'active').write_text(pod)
    elif a[:2] == ['get','pod']: sys.stdout.write((p/(a[2]+'.json')).read_text())
    elif a[0] == 'exec' and 'cat' in a: print('{"returncode":0}')
    elif a[0] == 'exec' and 'tar' in a: sys.stdout.buffer.write((p/(a[1]+'.tar')).read_bytes())
    elif a[0] == 'delete': (p/'active').unlink()
    elif a[0] == 'logs': print('synthetic cluster fixture; no live workload')
    elif a[:2] == ['get','events']: print('{}')
    else: sys.exit(2)
''')
        engine.chmod(0o755)
        result = self.cli("run", "--output", self.output, "--kubectl", engine)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        report = json.loads((self.output / "report.json").read_text())
        self.assertEqual(report["status"], "PASS_SMOKE")
        self.assertFalse(report["successful_controlled_comparison"])
        self.assertFalse((fixtures / "active").exists())
        calls = [json.loads(line) for line in (fixtures/"calls.jsonl").read_text().splitlines()]
        self.assertEqual(sum("create" in call for call in calls), 12)
        self.assertEqual(sum("delete" in call for call in calls), 12)
        self.assertEqual(len(report["executions"]), 12)

    def test_prepares_guaranteed_pairs_and_alternating_warmup_measured_sweep(self):
        result = self.prepare()
        self.assertEqual(result.returncode, 0, result.stderr)
        plan = json.loads((self.output / "plan.json").read_text())
        self.assertEqual(len(plan["executions"]), 24)
        self.assertEqual([(r["case"], r["phase"]) for r in plan["executions"][:6]],
                         [("a", "warmup"), ("b", "warmup"), ("b", "measured"),
                          ("a", "measured"), ("a", "measured"), ("b", "measured")])
        pods = [json.loads((self.output / r["manifest"]).read_text()) for r in plan["executions"]]
        self.assertEqual({p["spec"]["memNUMA"] for p in pods}, {0, 3})
        self.assertTrue(all(p["spec"]["cpuNUMA"] == 0 for p in pods))
        self.assertTrue(all(p["spec"]["nodeName"] == "worker" for p in pods))
        for pod in pods:
            resources = pod["spec"]["containers"][0]["resources"]
            self.assertEqual(resources["requests"], {"cpu": "2", "memory": "128Mi"})
            self.assertEqual(resources["requests"], resources["limits"])


if __name__ == "__main__":
    unittest.main()
