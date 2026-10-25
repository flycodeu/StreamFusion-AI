"""Isolated S4 Ops acceptance with real preview-node images (A/B and a failing C).

Requires an explicitly owned Linux lab, Docker socket, Docker CLI/Compose and
A.tar/B.tar/C.tar. Never targets a configured production controller. Platform's
authorization and reservation gate are separately covered by backend tests;
this harness acts as that authorized control client to exercise the Ops fence.
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
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from secure_request import protect_request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--lab', required=True)
    parser.add_argument('--node-ip', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    lab = Path(args.lab).resolve()
    assert os.geteuid() == 0 and str(lab).startswith('/opt/sf-preview-s4-')
    run = lab / ('run-' + uuid.uuid4().hex[:12])
    run.mkdir(mode=0o700)
    state, target = run / 'state', run / 'target'
    binary = str(Path(args.binary).resolve())
    password = 'Test' + secrets.token_hex(16)
    password_file = run / 'password'
    password_file.write_text(password)
    password_file.chmod(0o600)
    subprocess.run([binary, 'init-admin', '--state', str(state), '--password-file', str(password_file)], check=True)
    password_file.unlink()
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    log = (run / 'controller.log').open('w')
    cookies = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookies))
    csrf = ''
    checks, instance_id = [], ''
    docker = [str(lab / 'docker/docker'), '--host', 'unix://' + str(lab / 'docker.sock')]

    def start():
        process = subprocess.Popen([binary, 'serve', '--state', str(state), '--listen', f'127.0.0.1:{port}'], stdout=log, stderr=log)
        for _ in range(60):
            try:
                opener.open(base + '/ops/api/v1/health', timeout=1).close()
                return process
            except OSError:
                time.sleep(.1)
        raise AssertionError('controller failed to start')

    def api(path, method='GET', data=None, want=200):
        data = protect_request(path, method, data, lambda target: api('/auth/secure-challenge', 'POST', {'path': target}))
        request = urllib.request.Request(base + '/ops/api/v1' + path, None if data is None else json.dumps(data).encode(), method=method,
            headers={'Content-Type': 'application/json', 'Origin': base, 'X-CSRF-Token': csrf})
        try:
            response = opener.open(request, timeout=300)
        except urllib.error.HTTPError as error:
            response = error
        result = json.load(response)
        assert response.status == want, (path, response.status, result)
        return result['data']

    def login():
        nonlocal csrf
        csrf = api('/auth/csrf')['csrfToken']
        csrf = api('/auth/login', 'POST', {'username': 'admin', 'password': password})['csrfToken']

    def wait_job(job, desired='SUCCEEDED'):
        for _ in range(480):
            result = api('/jobs/' + job)
            if result['status'] in ('SUCCEEDED', 'FAILED', 'UNKNOWN'):
                assert result['status'] == desired, result
                if not result['targetAckPending']:
                    return result
            time.sleep(.5)
        raise AssertionError(('job timeout', job, result))

    def passed(message):
        checks.append(message)
        print(message, flush=True)

    def upload(version):
        path = lab / (version + '.tar')
        with path.open('rb') as source:
            sha = hashlib.file_digest(source, 'sha256').hexdigest()
        body = {'recipe': 'preview-node-v1', 'name': version, 'archiveSha256': sha, 'byteSize': path.stat().st_size, 'idempotencyKey': str(uuid.uuid4())}
        job = api('/artifact-imports', 'POST', body, 202)['jobId']
        connection = http.client.HTTPConnection('127.0.0.1', port, timeout=180)
        headers = {'Content-Type': 'application/octet-stream', 'Content-Length': str(path.stat().st_size), 'Origin': base,
            'X-CSRF-Token': csrf, 'Cookie': '; '.join(c.name + '=' + c.value for c in cookies)}
        with path.open('rb') as source:
            connection.request('PUT', '/ops/api/v1/jobs/' + job + '/input', source, headers)
            response = connection.getresponse()
            result = json.load(response)
            assert response.status == 200, result
        connection.close()
        api('/jobs/' + job + '/input/complete', 'POST', {}, 202)
        result = wait_job(job)
        passed('verified preview archive ' + version)
        return result['result']['artifactId']

    def node(path, data=None, want=200):
        identity = target / 'instances' / instance_id / 'preview-identity'
        context = ssl.create_default_context(cadata=ssl.DER_cert_to_PEM_cert((identity / 'node.der').read_bytes()))
        client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
        request = urllib.request.Request(f'https://{args.node_ip}:9443/preview-control/v1/' + path,
            None if data is None else json.dumps(data).encode(), headers={'Authorization': 'Bearer ' + (identity / 'machine-token').read_text().strip(), 'Content-Type': 'application/json'})
        try:
            response = client.open(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        result = json.load(response)
        assert response.status == want, (path, response.status, result)
        return result

    def maintenance(action, permit, want=200):
        snapshot = node('snapshot')
        return node('maintenance', {'protocolVersion': 1, 'nodeId': 's4-lab', 'bootId': snapshot['bootId'], 'configVersion': snapshot['configVersion'],
            'action': action, 'permitId': permit, 'jobId': None, 'deadline': int(time.time()*1000)+8000}, want)

    def current():
        return api('/instances/' + instance_id)

    def submit(artifact=None, kind='PUBLISH', permit='', desired='SUCCEEDED', wait=True):
        plan = api(f'/instances/{instance_id}/plans', 'POST', {'kind': kind, 'artifactId': artifact or '', 'maintenancePermit': permit})
        key = str(uuid.uuid4())
        job = api('/plans/' + plan['id'] + '/submit', 'POST', {'idempotencyKey': key}, 202)['jobId']
        assert api('/plans/' + plan['id'] + '/submit', 'POST', {'idempotencyKey': key})['jobId'] == job
        return plan, wait_job(job, desired) if wait else job

    process = start()
    try:
        login()
        controller = api('/settings')['controllerId']
        tid = api('/hosts', 'POST', {'name': 'S4 isolated', 'kind': 'LOCAL', 'installRoot': str(target)}, 201)['id']
        subprocess.run([binary, 'target-init', '--root', str(target), '--controller-id', controller, '--target-id', tid,
            '--docker-binary', str(lab / 'docker/docker'), '--docker-socket', str(lab / 'docker.sock')], check=True)
        plugins = target / 'docker-config/cli-plugins'
        plugins.mkdir(parents=True, mode=0o700)
        shutil.copy2(lab / 'docker-compose', plugins / 'docker-compose')
        host = api(f'/hosts/{tid}/checks', 'POST', {})
        assert host['capabilities']['mutation']['status'] == 'AVAILABLE'
        assert host['capabilities']['docker']['status'] == 'AVAILABLE'
        passed('protected executor and fixed preview recipe preflight')
        a, b, c = [upload(v) for v in ('A', 'B', 'C')]
        config = {'environment': 'preview', 'profile': '', 'bindAddress': args.node_ip, 'hostPort': 9443, 'containerPort': 9443,
            'memoryMiB': 8192, 'healthPath': '/preview-control/v1/snapshot', 'healthSeconds': 10, 'dataCompatibility': 'preview-ownership-v1', 'bindingIds': [],
            'preview': {'nodeId': 's4-lab', 'controlHost': args.node_ip, 'mediaAddress': args.node_ip, 'allowedTargets': ['192.0.2.10:554'], 'cpus': 8,
                'maxSources': 64, 'maxViewers': 64, 'maxTranscodes': 64, 'maxStarting': 4, 'ingressKbps': 655360, 'egressKbps': 655360, 'sourceUpperKbps': 8192, 'outputUpperKbps': 8192}}
        instance_id = api('/instances', 'POST', {'name': 'S4 preview isolation', 'targetId': tid, 'config': config})['id']
        plan_a, _ = submit(a)
        assert node('snapshot')['state'] == 'READY'
        assert node('snapshot')['maxSessions'] == node('snapshot')['maxTranscodes'] == 64
        registration = api(f'/instances/{instance_id}/preview-registration')
        assert registration['connection']['nodeId'] == 's4-lab' and 'machine-token' in registration['secureTransferFrom']
        assert (target / 'instances' / instance_id / 'preview-identity/machine-token').read_text().strip() not in json.dumps(registration)
        passed('A deployed with real ZLM/FFmpeg/control; 64/64; registration contains no credentials')
        api(f'/instances/{instance_id}/plans', 'POST', {'kind': 'PUBLISH', 'artifactId': b}, 409)
        boot = node('snapshot')['bootId']
        _, invalid = submit(b, permit=str(uuid.uuid4()), desired='FAILED')
        assert invalid['errorCode'] == 'MAINTENANCE_PERMIT_MISMATCH' and node('snapshot')['bootId'] == boot
        passed('missing/wrong permit rejected without restarting current node')
        permit = str(uuid.uuid4())
        maintenance('PREPARE', permit)
        plan_b, success = submit(b, permit=permit)
        assert current()['currentReleaseId'] == plan_b['candidate']['id']
        assert current()['previousReleaseId'] == plan_a['candidate']['id']
        assert node('snapshot')['state'] == 'MAINTENANCE' and node('maintenance')['state'] == 'COMPLETE'
        assert success['result']['preview']['maxTranscodes'] == 64
        maintenance('RELEASE', permit)
        passed('A to B upgrade preserves fence and identity, requires explicit release')
        permit = str(uuid.uuid4()); maintenance('PREPARE', permit)
        _, failed = submit(c, permit=permit, desired='FAILED')
        assert failed['phase'] == 'BASELINE_RESTORED' and current()['currentReleaseId'] == plan_b['candidate']['id']
        assert node('snapshot')['state'] == 'MAINTENANCE'
        maintenance('RELEASE', permit)
        passed('unhealthy C restores B; failed candidate never changes successful release slots')
        permit = str(uuid.uuid4()); maintenance('PREPARE', permit)
        rollback, _ = submit(kind='ROLLBACK', permit=permit)
        assert rollback['candidate']['id'] == plan_a['candidate']['id'] and current()['previousReleaseId'] == plan_b['candidate']['id']
        maintenance('RELEASE', permit)
        passed('explicit rollback restores frozen A configuration and keeps 64/64')
        permit = str(uuid.uuid4()); maintenance('PREPARE', permit)
        crash_plan, crash_job = submit(c, permit=permit, wait=False)
        for _ in range(120):
            result = subprocess.run(docker + ['inspect', '--format', '{{index .Config.Labels "com.streamfusion.ops.release"}}', 'sfops-' + instance_id.replace('-', '')], capture_output=True, text=True)
            if result.stdout.strip() == crash_plan['candidate']['id']:
                break
            time.sleep(.2)
        else:
            raise AssertionError('candidate not activated')
        process.kill(); process.wait(timeout=10)
        process = start(); login()
        assert api('/jobs/' + crash_job)['status'] == 'UNKNOWN'
        for _ in range(120):
            if (target / 'jobs' / crash_job / 'result.json').exists():
                break
            time.sleep(.5)
        api('/jobs/' + crash_job + '/reconcile', 'POST', {})
        wait_job(crash_job, 'FAILED')
        assert current()['currentReleaseId'] == plan_a['candidate']['id']
        assert node('maintenance')['state'] == 'COMPLETE'
        maintenance('RELEASE', permit)
        passed('controller crash retains UNKNOWN/locks; original job reconciles and finishes maintenance')
        report = {'status': 'PASS', 'checks': checks, 'runDirectory': str(run), 'instanceId': instance_id,
            'scope': 'local WSL isolated Docker and real preview appliance; no independent Linux host or browser media acceptance',
            'finalSnapshot': node('snapshot'), 'finalInstance': current()}
        Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
        print(json.dumps({'status': 'PASS', 'checks': len(checks), 'output': args.output}), flush=True)
    finally:
        process.terminate(); process.wait(timeout=20); log.close()
        if instance_id:
            subprocess.run(docker + ['rm', '-f', 'sfops-' + instance_id.replace('-', '')], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
