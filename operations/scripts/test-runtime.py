"""Real HTTP/CLI smoke test against an isolated Linux systemd controller."""
import argparse
import http.cookiejar
import json
import pathlib
import subprocess
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument("--url", required=True)
parser.add_argument("--binary", required=True)
parser.add_argument("--state", required=True)
parser.add_argument("--password-file", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--unit", required=True)
args = parser.parse_args()
root = pathlib.Path(args.output)
root.mkdir(parents=True, exist_ok=True)
password = pathlib.Path(args.password_file).read_text().strip()
jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.ProxyHandler({}))
csrf = ""
checks = []
def request(path, method="GET", data=None, expected=200, binary=False):
    body = None if data is None else json.dumps(data).encode()
    req = urllib.request.Request(args.url + "/ops/api/v1" + path, data=body, method=method,
        headers={"Content-Type": "application/json", "Origin": args.url, "X-CSRF-Token": csrf})
    try:
        response = opener.open(req, timeout=15)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        content = response.read()
        assert response.status == expected, (path, response.status, content[:300])
        assert len(response.headers["X-Trace-Id"]) == 32
        if binary:
            return content
        result = json.loads(content)
        assert result["traceId"] == response.headers["X-Trace-Id"]
        return result["data"]
def command(*params, ok=True):
    result = subprocess.run([args.binary, *params], text=True, capture_output=True, timeout=20)
    if ok:
        assert result.returncode == 0, result.stderr
    else:
        assert result.returncode != 0
    return result
def login():
    global csrf
    csrf = request("/auth/csrf")["csrfToken"]
    csrf = request("/auth/login", "POST", {"username": "admin", "password": password})["csrfToken"]
request("/status", expected=401)
login()
before = request("/settings")
assert request("/status")["os"] == "linux"
request("/settings", "PUT", {"name": "Linux I01", "version": before["version"]})
credential = request("/credentials", "POST", {"name": "Temporary test", "kind": "TOKEN", "secret": "temporary-token-never-publish"}, expected=201)
assert "secret" not in json.dumps(request("/credentials"))
checks.extend(["real-login", "linux-status", "settings-write", "protected-credential"])
archive = root / "online-backup.tar.gz"
archive.write_bytes(request("/backups", "POST", binary=True))
checks.append("online-consistent-backup")
occupied = command("serve", "--state", str(root / "port-conflict"), "--listen", args.url.removeprefix("http://"), ok=False)
# Initialize a second state to reach the port binding guard.
temp_password = pathlib.Path(args.password_file)
command("init-admin", "--state", str(root / "port-conflict"), "--password-file", str(temp_password))
occupied = command("serve", "--state", str(root / "port-conflict"), "--listen", args.url.removeprefix("http://"), ok=False)
assert "LISTEN_FAILED" in occupied.stderr
locked = command("check-state", "--state", args.state, ok=False)
assert "STATE_LOCKED" in locked.stderr
checks.extend(["port-conflict", "directory-lock"])
subprocess.run(["systemctl", "restart", args.unit], check=True)
import time
for attempt in range(30):
    try:
        if request("/health")["status"] == "UP":
            break
    except Exception:
        time.sleep(0.2)
else:
    raise AssertionError("controller did not restart")
after = request("/settings")
assert before["controllerId"] == after["controllerId"] and after["name"] == "Linux I01"
checks.append("restart-preserves-account-identity-session-settings")
subprocess.run(["systemctl", "stop", args.unit], check=True)
key = root / "recovery.key"
command("export-key", "--state", args.state, "--out", str(key))
command("restore", "--archive", str(archive), "--key", str(key), "--state", str(root / "restored"))
command("check-state", "--state", str(root / "restored"))
command("restore", "--archive", str(archive), "--key", str(key), "--state", str(root / "restored"), ok=False)
checks.extend(["separate-key-recovery", "existing-destination-rejected"])
print(json.dumps({"passed": checks, "os": "Ubuntu 24.04 / systemd", "scope": "isolated I01 controller"}, ensure_ascii=False))
