"""Linux/Docker release acceptance against isolated, prebuilt Java fixtures.

Requires root, an isolated Docker socket, Compose plugin and three Docker-save
archives named A.tar/B.tar/C.tar. C must return health DOWN. No business data is
created and no existing Docker service is stopped.
"""
import argparse
import hashlib
import http.client
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--binary", required=True)
    p.add_argument("--lab", required=True)
    p.add_argument("--output", required=True)
    args = p.parse_args()
    assert os.geteuid() == 0
    lab = Path(args.lab).resolve()
    assert str(lab).startswith("/opt/")
    run = lab / ("run-" + uuid.uuid4().hex[:12])
    run.mkdir(mode=0o700)
    binary = str(Path(args.binary).resolve())
    state, target = run / "state", run / "target"
    password = "Test" + secrets.token_hex(8)
    password_file = run / "password"
    password_file.write_text(password)
    password_file.chmod(0o600)
    subprocess.run([binary, "init-admin", "--state", str(state), "--password-file", str(password_file)], check=True)
    password_file.unlink()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    log = (run / "controller.log").open("w")
    def start():
        process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}"], stdout=log, stderr=log)
        for _ in range(40):
            try:
                urllib.request.urlopen(base + "/ops/api/v1/health", timeout=1).close()
                return process
            except OSError:
                time.sleep(.1)
        raise AssertionError("controller did not start")
    process = start()
    cookies = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
    csrf = ""
    checks = []
    instance_id = ""

    def api(path, method="GET", data=None, want=200):
        body = None if data is None else json.dumps(data).encode()
        req = urllib.request.Request(base + "/ops/api/v1" + path, body, method=method,
            headers={"Content-Type": "application/json", "Origin": base, "X-CSRF-Token": csrf})
        try:
            response = opener.open(req, timeout=300)
        except urllib.error.HTTPError as error:
            response = error
        result = json.load(response)
        assert response.status == want, (path, response.status, result)
        return result["data"]

    def login():
        nonlocal csrf
        csrf = api("/auth/csrf")["csrfToken"]
        csrf = api("/auth/login", "POST", {"username": "admin", "password": password})["csrfToken"]

    def wait_job(job_id, desired="SUCCEEDED"):
        for _ in range(240):
            j = next(j for j in api("/jobs") if j["id"] == job_id)
            if j["status"] in ("SUCCEEDED", "FAILED", "UNKNOWN"):
                assert j["status"] == desired, j
                if not j["targetAckPending"]:
                    return j
            time.sleep(.5)
        raise AssertionError("job timeout: " + job_id)

    def current():
        return next(i for i in api("/instances") if i["id"] == instance_id)

    def upload_archive(version):
        path = lab / (version + ".tar")
        sha = hashlib.file_digest(path.open("rb"), "sha256").hexdigest()
        body = {"name": version, "archiveSha256": sha, "byteSize": path.stat().st_size, "idempotencyKey": str(uuid.uuid4())}
        job = api("/artifact-imports", "POST", body, 202)["jobId"]
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=180)
        headers = {"Content-Type": "application/octet-stream", "Content-Length": str(path.stat().st_size), "Origin": base,
                   "X-CSRF-Token": csrf, "Cookie": "; ".join(c.name + "=" + c.value for c in cookies)}
        with path.open("rb") as source:
            connection.request("PUT", "/ops/api/v1/jobs/" + job + "/input", source, headers)
            response = connection.getresponse()
            result = json.load(response)
            assert response.status == 200, result
        connection.close()
        api("/jobs/" + job + "/input/complete", "POST", {}, 202)
        j = wait_job(job)
        checks.append("import " + version)
        return j["result"]["artifactId"]

    def publish(artifact=None, kind="PUBLISH", expected="SUCCEEDED"):
        plan = api(f"/instances/{instance_id}/plans", "POST", {"kind": kind, "artifactId": artifact or ""})
        assert api(f"/plans/{plan['id']}")["inputHash"] == plan["inputHash"]
        key = str(uuid.uuid4())
        accepted = api(f"/plans/{plan['id']}/submit", "POST", {"idempotencyKey": key}, 202)
        repeated = api(f"/plans/{plan['id']}/submit", "POST", {"idempotencyKey": key})
        assert repeated["jobId"] == accepted["jobId"]
        j = wait_job(accepted["jobId"], expected)
        assert api(f"/jobs/{accepted['jobId']}")["status"] == expected
        api(f"/plans/{plan['id']}/submit", "POST", {"idempotencyKey": str(uuid.uuid4())}, 409)
        return plan, j

    try:
        login()
        cid = api("/settings")["controllerId"]
        tid = api("/targets", "POST", {"name": "lab", "installRoot": str(target)}, 201)["id"]
        subprocess.run([binary, "target-init", "--root", str(target), "--controller-id", cid, "--target-id", tid,
            "--docker-binary", str(lab / "docker/docker"), "--docker-socket", str(lab / "docker.sock")], check=True)
        plugins = target / "docker-config/cli-plugins"
        plugins.mkdir(parents=True, mode=0o700)
        shutil.copy2(lab / "docker-compose", plugins / "docker-compose")
        api(f"/targets/{tid}/probes", "POST", {})
        checks.append("protected target bootstrap and probe")
        artifact_a, artifact_b, artifact_c = [upload_archive(v) for v in ("A", "B", "C")]
        config = {"environment": "test", "profile": "", "bindAddress": "127.0.0.1", "hostPort": 28190, "containerPort": 8080,
                  "memoryMiB": 512, "healthPath": "/actuator/health", "healthSeconds": 8, "dataCompatibility": "fixture-1", "bindingIds": []}
        instance_id = api("/instances", "POST", {"name": "Java acceptance", "targetId": tid, "config": config})["id"]
        plan_a, _ = publish(artifact_a)
        assert current()["currentReleaseId"] == plan_a["candidate"]["id"]
        checks.append("A health and identity verified")
        plan_b, _ = publish(artifact_b)
        assert current()["currentReleaseId"] == plan_b["candidate"]["id"]
        assert current()["previousReleaseId"] == plan_a["candidate"]["id"]
        checks.append("B current, A previous; repeated submit is idempotent")
        _, failed = publish(artifact_c, expected="FAILED")
        assert failed["phase"] == "BASELINE_RESTORED"
        assert current()["currentReleaseId"] == plan_b["candidate"]["id"]
        assert current()["previousReleaseId"] == plan_a["candidate"]["id"]
        checks.append("C failed, B restored; successful slots unchanged")
        _, _ = publish(kind="ROLLBACK")
        assert current()["currentReleaseId"] == plan_a["candidate"]["id"]
        assert current()["previousReleaseId"] == plan_b["candidate"]["id"]
        checks.append("rollback swaps A/B after actual health")
        api(f"/instances/{instance_id}/observations", "POST", {})
        assert current()["observation"]["releaseId"] == plan_a["candidate"]["id"]
        assert "fixture A started" in api(f"/instances/{instance_id}/logs")["text"]
        checks.append("actual observation and bounded logs")
        process.terminate()
        process.wait(timeout=20)
        process = start()
        login()
        assert current()["currentReleaseId"] == plan_a["candidate"]["id"]
        checks.append("controller restart preserves slots and running service")
        crash_plan = api(f"/instances/{instance_id}/plans", "POST", {"kind": "PUBLISH", "artifactId": artifact_c})
        crash_job = api(f"/plans/{crash_plan['id']}/submit", "POST", {"idempotencyKey": str(uuid.uuid4())}, 202)["jobId"]
        docker = [str(lab / "docker/docker"), "--host", "unix://" + str(lab / "docker.sock")]
        for _ in range(100):
            observed = subprocess.run(docker + ["inspect", "--format", '{{index .Config.Labels "com.streamfusion.ops.release"}}', "sfops-" + instance_id.replace("-", "")], capture_output=True, text=True)
            if observed.stdout.strip() == crash_plan["candidate"]["id"]:
                break
            time.sleep(.1)
        else:
            raise AssertionError("candidate not activated before crash")
        process.kill()
        process.wait(timeout=10)
        process = start()
        login()
        assert next(j for j in api("/jobs") if j["id"] == crash_job)["status"] == "UNKNOWN"
        api(f"/plans/{crash_plan['id']}/submit", "POST", {"idempotencyKey": str(uuid.uuid4())}, 409)
        for _ in range(40):
            result_file = target / "jobs" / crash_job / "result.json"
            if result_file.exists():
                break
            time.sleep(.5)
        api(f"/jobs/{crash_job}/reconcile", "POST", {})
        wait_job(crash_job, "FAILED")
        assert current()["currentReleaseId"] == plan_a["candidate"]["id"]
        checks.append("crash during activated C: UNKNOWN blocks writes; original job reconciles restored A")
        process.terminate()
        process.wait(timeout=20)
        with sqlite3.connect(state / "controller.sqlite") as db:
            db.execute("UPDATE jobs SET target_ack_pending=1 WHERE id=?", (crash_job,))
            db.execute("INSERT INTO operation_locks VALUES(?,?)", ("target:" + tid, crash_job))
            db.execute("INSERT INTO operation_locks VALUES(?,?)", ("instance:" + instance_id, crash_job))
        process = start()
        login()
        wait_job(crash_job, "FAILED")
        assert current()["appliedGeneration"] == "3"
        checks.append("lost ACK receipt recovered after restart without another activation or generation change")
        original_instance = instance_id
        instance_id = api("/instances", "POST", {"name": "Port conflict acceptance", "targetId": tid, "config": config})["id"]
        not_started = api(f"/instances/{instance_id}/plans", "POST", {"kind": "PUBLISH", "artifactId": artifact_a})
        rejected_job = api(f"/plans/{not_started['id']}/submit", "POST", {"idempotencyKey": str(uuid.uuid4())}, 202)["jobId"]
        wait_job(rejected_job, "UNKNOWN")
        assert not (target / "jobs" / rejected_job / "intent.json").exists()
        api(f"/jobs/{rejected_job}/reconcile", "POST", {})
        j = wait_job(rejected_job, "FAILED")
        assert j["phase"] == "NOT_STARTED" and current()["appliedGeneration"] == "0"
        checks.append("port conflict before intent: UNKNOWN safely converges after empty-baseline proof; no activation")
        instance_id = original_instance
        images = {a["id"]: a["imageId"] for a in api("/artifacts")}
        subprocess.run(docker + ["tag", images[artifact_b], "sfops/fixture:a"], check=True)
        try:
            _, collision = publish(artifact_a, expected="FAILED")
            assert collision["errorCode"] == "IMAGE_TAG_COLLISION"
            assert current()["appliedGeneration"] == "3"
            assert subprocess.check_output(docker + ["image", "inspect", "--format", "{{.Id}}", "sfops/fixture:a"], text=True).strip() == images[artifact_b]
        finally:
            subprocess.run(docker + ["tag", images[artifact_a], "sfops/fixture:a"], check=True)
        checks.append("external tag collision rejected before load; existing tag and successful slots preserved")
        report = {"checks": checks, "runDirectory": str(run), "status": "PASS", "scope": "isolated Java fixture, not StreamFusion business backend"}
        Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print(json.dumps(report, ensure_ascii=False))
    finally:
        process.terminate()
        process.wait(timeout=20)
        log.close()
        if instance_id:
            subprocess.run([str(lab / "docker/docker"), "--host", "unix://" + str(lab / "docker.sock"), "rm", "-f",
                            "sfops-" + instance_id.replace("-", "")], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
