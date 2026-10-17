#!/usr/bin/env python3
"""Prepare one node's files locally; never send credentials to a browser or remote service."""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
from urllib.parse import urlsplit


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--node-id', required=True)
    parser.add_argument('--name', required=True)
    parser.add_argument('--media-ip', required=True)
    parser.add_argument('--control-host', required=True)
    parser.add_argument('--allow', action='append', required=True, help='Numeric camera-IP:RTSP-port; repeat for each endpoint')
    parser.add_argument('--max-sessions', type=int, default=64)
    parser.add_argument('--max-transcodes', type=int, default=64)
    parser.add_argument('--max-sources', type=int, default=64)
    parser.add_argument('--max-starting', type=int, default=4)
    parser.add_argument('--source-upper-kbps', type=int, default=8192)
    parser.add_argument('--output-upper-kbps', type=int, default=8192)
    parser.add_argument('--ingress-kbps', type=int, default=655360)
    parser.add_argument('--egress-kbps', type=int, default=655360)
    args = parser.parse_args()
    if not 1 <= args.max_sources <= 64 or not 1 <= args.max_starting <= 4 or not 1 <= args.source_upper_kbps <= args.output_upper_kbps <= 1000000 or not 1 <= args.ingress_kbps <= 100000000 or not 1 <= args.egress_kbps <= 100000000:
        parser.error('Invalid source, startup or bandwidth budget')
    if args.node_id == 'local' or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', args.node_id) or not 1 <= len(args.name) <= 80:
        parser.error('Invalid node ID or name')
    media = ipaddress.IPv4Address(args.media_ip)
    if media.is_unspecified or media.is_multicast:
        parser.error('A concrete browser-reachable media IPv4 address is required')
    if not re.fullmatch(r'[A-Za-z0-9.-]{1,253}', args.control_host):
        parser.error('Invalid TLS hostname')
    for endpoint in args.allow:
        uri = urlsplit('rtsp://' + endpoint)
        ipaddress.ip_address(uri.hostname)
        if not uri.port or uri.path or uri.username or uri.query or uri.fragment:
            parser.error('Each allowed endpoint must be numeric IP:port')
    if not 1 <= args.max_sessions <= 64 or not 0 <= args.max_transcodes <= 64 or len(args.allow) > 256:
        parser.error('Invalid preview budgets')
    root = Path(__file__).resolve().parent
    image = next(line.partition('=')[2] for line in (root / '.env').read_text().splitlines() if line.startswith('PREVIEW_IMAGE='))
    config = root / 'config'
    config.mkdir(mode=0o700)  # Existing configuration is never overwritten.
    for name in ('machine-token', 'tls-password', 'zlm-secret'):
        (config / name).write_text(secrets.token_hex(32))
        (config / name).chmod(0o600)
    try:
        ipaddress.IPv4Address(args.control_host)
        san = 'IP:' + args.control_host
    except ValueError:
        san = 'DNS:' + args.control_host
    subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'keytool', '-v', f'{config}:/config', image,
                    '-genkeypair', '-alias', 'node', '-keyalg', 'EC', '-groupname', 'secp256r1', '-dname', 'CN=' + args.node_id,
                    '-ext', 'SAN=' + san, '-validity', '365', '-storetype', 'PKCS12', '-keystore', '/config/identity.p12',
                    '-storepass:file', '/config/tls-password', '-noprompt'], check=True, timeout=30, stdout=subprocess.DEVNULL)
    certificate = subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'keytool', '-v', f'{config}:/config:ro', image,
                    '-exportcert', '-alias', 'node', '-keystore', '/config/identity.p12', '-storepass:file', '/config/tls-password'],
                    check=True, timeout=15, capture_output=True).stdout
    (config / 'node.json').write_text(json.dumps({
        'nodeId': args.node_id, 'bindHost': '0.0.0.0', 'controlPort': 9443,
        'keyStore': '/config/identity.p12', 'keyStorePasswordFile': '/config/tls-password', 'tokenFile': '/config/machine-token',
        'workDir': '/state/ownership', 'zlmApi': 'http://127.0.0.1:8080', 'zlmExecutable': '/opt/media/MediaServer',
        'zlmConfig': '/state/config.ini', 'zlmSecretFile': '/config/zlm-secret', 'ffmpegExecutable': '/usr/bin/ffmpeg',
        'mediaAddress': str(media), 'allowedTargets': args.allow, 'maxSessions': args.max_sessions, 'maxTranscodes': args.max_transcodes,
        'budget': {'maxSources': args.max_sources, 'maxViewers': args.max_sessions, 'maxTranscodes': args.max_transcodes,
                   'maxStarting': args.max_starting, 'sourceUpperKbps': args.source_upper_kbps, 'outputUpperKbps': args.output_upper_kbps,
                   'ingressKbps': args.ingress_kbps, 'egressKbps': args.egress_kbps},
    }, ensure_ascii=False, indent=2) + '\n')
    platform = root / 'platform-registration'
    platform.mkdir(mode=0o700)
    (platform / 'machine-token').write_bytes((config / 'machine-token').read_bytes())
    (platform / 'machine-token').chmod(0o600)
    (platform / 'connection.json').write_text(json.dumps({
        'nodeId': args.node_id, 'name': args.name, 'controlUri': f'https://{args.control_host}:9443',
        'certificateSha256': hashlib.sha256(certificate).hexdigest(),
        'tokenFile': '/REPLACE/with/protected/platform/machine-token', 'mediaAddress': str(media),
    }, ensure_ascii=False, indent=2) + '\n')
    if os.geteuid() == 0:
        for path in [config, *config.iterdir()]:
            os.chown(path, 10001, 10001)
            path.chmod(0o700 if path.is_dir() else 0o600)
    else:
        raise SystemExit('Configuration generated; set config ownership to UID/GID 10001 before starting (see README)')
    print('Prepared node configuration. Set PREVIEW_BIND_IP in .env; transfer platform-registration securely and adjust tokenFile.')


if __name__ == '__main__':
    main()
