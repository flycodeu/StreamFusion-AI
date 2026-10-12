"""Real Linux/Docker plugin acceptance on an explicitly isolated Docker socket.

Requires Ubuntu 24.04, root, sudo and a protected Docker executable. It creates
only a fresh controller/target under --lab; it never installs/starts Docker or
uses the host's default socket. Go fixture tests are separate from this test.
"""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--lab", required=True)
    parser.add_argument("--docker-binary", required=True)
    parser.add_argument("--docker-socket", required=True)
    args = parser.parse_args()
    if not hasattr(os, "geteuid") or os.geteuid() != 0:
        raise SystemExit("Ubuntu Linux root is required")
    lab = Path(args.lab).resolve()
    if lab.parent != Path("/opt") or not lab.is_dir():
        raise SystemExit("--lab must be an existing dedicated directory under /opt")
    docker_socket = Path(args.docker_socket).resolve()
    if not docker_socket.is_relative_to(lab):
        raise SystemExit("Docker socket must be inside the dedicated lab")
    docker = str(Path(args.docker_binary).resolve())
    binary = str(Path(args.binary).resolve())

    def docker_call(*command, check=True):
        return subprocess.run([docker, "--host", "unix://" + str(docker_socket), *command],
                              capture_output=True, text=True, timeout=600, check=check)

    docker_root = Path(docker_call("info", "--format", "{{.DockerRootDir}}").stdout.strip()).resolve()
    if not docker_root.is_relative_to(lab):
        raise SystemExit("Docker data root must be inside the dedicated lab")
    if docker_call("ps", "-aq").stdout.strip():
        raise SystemExit("Use an isolated Docker daemon with no existing containers")
    run = lab / ("plugin-run-" + uuid.uuid4().hex[:12])
    run.mkdir(mode=0o700)
    state, target = run / "state", run / "target"
    password = "Test" + secrets.token_hex(8)
    password_file = run / "password.secret"
    password_file.write_text(password)
    password_file.chmod(0o600)
    try:
        subprocess.run([binary, "init-admin", "--state", str(state), "--password-file", str(password_file)], check=True)
    finally:
        password_file.unlink(missing_ok=True)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    log = (run / "controller.log").open("w")
    process = None
    csrf = ""
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    checks = []

    def api(path, method="GET", data=None, want=200):
        body = None if data is None else json.dumps(data).encode()
        req = urllib.request.Request(base + "/ops/api/v1" + path, body, method=method,
                                     headers={"Content-Type": "application/json", "Origin": base, "X-CSRF-Token": csrf})
        try:
            response = opener.open(req, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            result = json.load(response)
            assert response.status == want, (path, response.status, result)
        return result

    def start():
        nonlocal process, csrf
        process = subprocess.Popen([binary, "serve", "--state", str(state), "--listen", f"127.0.0.1:{port}"], stdout=log, stderr=log)
        for _ in range(50):
            try:
                with urllib.request.urlopen(base + "/ops/api/v1/health", timeout=1):
                    break
            except OSError:
                time.sleep(.1)
        else:
            raise AssertionError("controller failed to start")
        csrf = api("/auth/csrf")["data"]["csrfToken"]
        csrf = api("/auth/login", "POST", {"username": "admin", "password": password})["data"]["csrfToken"]

    def stop():
        nonlocal process
        if process is not None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            process = None

    def wait_job(job_id):
        deadline = time.monotonic() + 900
        while time.monotonic() < deadline:
            job = api("/jobs/" + job_id)["data"]
            if job["status"] in ("SUCCEEDED", "FAILED", "UNKNOWN"):
                assert job["status"] == "SUCCEEDED", job
                if not job["targetAckPending"]:
                    return job
            time.sleep(1)
        raise AssertionError("job did not finish: " + job_id)

    try:
        start()
        target_id = api("/targets", "POST", {"name": "plugin acceptance", "installRoot": str(target)}, 201)["data"]["id"]
        definitions = api("/plugins")["data"]
        java17 = next(d for d in definitions if d["version"] == "17-jre")
        missing = api("/targets/" + target_id + "/plugin-install-plans", "POST", {
            "pluginKey": java17["key"], "version": java17["version"], "recipeRevision": java17["recipeRevision"]}, 409)
        assert missing["code"] == "DOCKER_NOT_READY", missing
        checks.append("uninstalled-executor-blocked")
        cid = api("/settings")["data"]["controllerId"]
        subprocess.run([binary, "target-init", "--root", str(target), "--controller-id", cid, "--target-id", target_id,
                        "--docker-binary", docker, "--docker-socket", str(docker_socket)], check=True)
        # Reuse an image prepared outside Ops and verify uninstall preserves it.
        java17_ref = java17["image"] + "@" + java17["digest"]
        docker_call("pull", "--platform", "linux/amd64", java17_ref)
        ids = {}
        for definition in definitions:
            plan = api("/targets/" + target_id + "/plugin-install-plans", "POST", {
                "pluginKey": definition["key"], "version": definition["version"], "recipeRevision": definition["recipeRevision"]})["data"]
            key = str(uuid.uuid4())
            accepted = api("/plugin-install-plans/" + plan["id"] + "/submit", "POST", {"idempotencyKey": key}, 202)["data"]
            duplicate = api("/plugin-install-plans/" + plan["id"] + "/submit", "POST", {"idempotencyKey": key})["data"]
            assert duplicate == accepted
            wait_job(accepted["jobId"])
            installation_id = accepted["pluginInstallationId"]
            item = api("/plugin-installations/" + installation_id)["data"]
            assert item["status"] == "READY" and item["imageDigest"] == definition["digest"] and item["observedVersion"]
            assert api("/jobs/" + accepted["jobId"] + "/logs")["data"]["text"]
            assert api("/plugin-installations/" + installation_id + "/verify", "POST")["data"]["status"] == "READY"
            ids[definition["version"]] = installation_id
            checks.append("installed-verified-" + definition["version"])
        assert len(api("/plugin-installations?targetId=" + target_id)["data"]) == len(definitions)
        checks.append("multi-version-coexistence-and-idempotency")
        stop()
        start()
        assert all(row["status"] == "READY" for row in api("/plugin-installations")["data"])
        checks.append("controller-restart-keeps-installations")
        key = str(uuid.uuid4())
        removed = api("/plugin-installations/" + ids["17-jre"] + "/remove", "POST", {"idempotencyKey": key}, 202)["data"]
        wait_job(removed["jobId"])
        api("/plugin-installations/" + ids["17-jre"], want=404)
        duplicate = api("/plugin-installations/" + ids["17-jre"] + "/remove", "POST", {"idempotencyKey": key})["data"]
        assert duplicate["jobId"] == removed["jobId"]
        docker_call("image", "inspect", java17_ref)
        checks.append("uninstall-preserves-external-image")
        result = {"passed": checks, "backend": "real binary / isolated Docker daemon", "productionAcceptance": False}
        (run / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2))
        print(json.dumps(result, ensure_ascii=False))
    finally:
        stop()
        log.close()


if __name__ == "__main__":
    main()
