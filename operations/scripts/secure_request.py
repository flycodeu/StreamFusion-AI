"""Protect developer HTTP fixtures using the real browser crypto implementation.

Requires Node 24 on the test runner, never on the deployed controller.
Secrets are passed over stdin, not process arguments or temporary files.
"""
import json
from pathlib import Path
import re
import subprocess


def protect_request(path, method, data, issue):
    if method != "POST" or not (
        path in ("/auth/login", "/auth/password", "/credentials")
        or re.fullmatch(r"/credentials/[0-9a-fA-F-]{36}/revisions", path)
        or re.fullmatch(r"/hosts/[0-9a-fA-F-]{36}/preparation/install", path)
    ):
        return data
    challenge = issue(path)
    result = subprocess.run(
        ["node", str(Path(__file__).with_name("encrypt-request.mjs"))],
        input=json.dumps({"challenge": challenge, "path": path, "data": data}),
        text=True, encoding="utf8", capture_output=True, timeout=15,
    )
    if result.returncode:
        raise RuntimeError("敏感请求加密失败，请检查测试机的 Node 24 环境")
    return json.loads(result.stdout)
