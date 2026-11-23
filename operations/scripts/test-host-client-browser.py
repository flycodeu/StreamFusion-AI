"""Exercise the built Ops UI with Chromium and a target-free controller fixture."""

import argparse
import secrets
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlsplit

from playwright.sync_api import expect, sync_playwright


DIST = Path(__file__).resolve().parents[1] / "web" / "dist"
HOST = "11111111-1111-4111-8111-111111111111"
MYSQL = "22222222-2222-4222-8222-222222222221"
REDIS = "22222222-2222-4222-8222-222222222222"
NATIVE = "22222222-2222-4222-8222-222222222223"
POSTGRES = "22222222-2222-4222-8222-222222222224"
MARIA = "22222222-2222-4222-8222-222222222225"
JOB = "33333333-3333-4333-8333-333333333333"
INSTALL_JOB = "33333333-3333-4333-8333-333333333334"
LOCAL = "11111111-1111-4111-8111-111111111112"
STAMP = "2026-11-21T09:00:00Z"
ADMIN = "44444444-4444-4444-8444-444444444441"
ACCOUNT_PATH = f"/software-instances/{MYSQL}/login-accounts"


class StaticHandler(SimpleHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass


def instance(instance_id, kind, runtime, name, locator, port):
    config = {"bindAddress": "0.0.0.0", "hostPort": port}
    if runtime == "SYSTEMD":
        config["runtimeMode"] = "NATIVE"
    if kind == "MYSQL":
        config.update(username="operator", database="warehouse")
    return {
        "id": instance_id, "name": name, "hostId": HOST, "hostName": "fixture-ubuntu",
        "managementMode": "MANAGED", "softwareType": kind, "runtimeKind": runtime,
        "locator": locator, "installationStatus": "READY", "actualState": "RUNNING",
        "desiredState": "RUNNING", "version": "4", "freshness": "FRESH",
        "softwareVersion": "8.4.11" if kind == "MYSQL" else "7.4.11", "dataOwnership": "OPS",
        "lastJobId": "", "createdAt": STAMP, "updatedAt": STAMP,
        "installationPaths": {"root": "/srv/store/cache"},
        "installation": {"definition": {"key": kind.lower(), "name": kind}, "config": config},
        "observation": {"state": "RUNNING", "version": kind, "observedAt": STAMP, "source": "EXECUTOR"},
    }


class Fixture:
    def __init__(self):
        self.instances = [
            instance(MYSQL, "MYSQL", "DOCKER", "旧版 MySQL", "sf-mysql", 13306),
            instance(REDIS, "REDIS", "DOCKER", "旧版 Redis", "sf-redis", 16379),
            instance(NATIVE, "REDIS", "SYSTEMD", "原生 Redis", "redis-fixture.service", 26379),
        ]
        self.instances[1]["observation"].update(
            version="sha256:" + "c" * 64, versionSource="IMAGE_REFERENCE", versionStatus="IMAGE_ONLY",
        )
        self.instances[1]["installationEvidence"] = {"observedVersion": "Redis server v=7.2.4", "observedAt": STAMP}
        for instance_id, name, kind, image, port, target_port in [
            (POSTGRES, "已登记 PostgreSQL", "OTHER", "postgres:17", 15432, 5432),
            (MARIA, "已登记 MariaDB", "MYSQL", "mariadb:11", 13308, 3306),
        ]:
            item = instance(instance_id, kind, "DOCKER", name, "a" * 64, port)
            del item["installation"]
            item.update(managementMode="OBSERVE", installationStatus="")
            item["observation"].update(version=image, portMappings=[f"127.0.0.1:{port} → {target_port}/tcp"])
            self.instances.append(item)
        self.hosts = [{"id": HOST, "name": "fixture-ubuntu", "kind": "SSH", "sshUser": "ops", "version": "4", "maintenance": False},
                      {"id": LOCAL, "name": "fixture-local", "kind": "LOCAL", "version": "7", "maintenance": False}]
        ready = {"status": "AVAILABLE", "message": "", "code": "", "source": "EXECUTOR", "observedAt": STAMP}
        self.hosts[0].update(address="127.0.0.1", installRoot="/opt/sf-qa", capabilities={
            **dict.fromkeys(("reachability", "readOnly", "mutation", "docker"), ready), "expiresAt": "2099-01-01T00:00:00Z",
            "distribution": "ubuntu", "distributionVersion": "24.04", "platform": "linux/amd64"})
        self.recipe = {"key": "nginx", "name": "Nginx", "version": "1", "revision": "nginx-fixture", "kind": "SERVICE",
                       "port": 80, "image": "nginx:fixture", "imageId": "sha256:" + "a" * 64, "digest": "sha256:" + "b" * 64,
                       "runtimeModes": ["DOCKER"], "sourceModes": ["REGISTRY"], "platform": "linux/amd64"}
        self.component_submits = 0
        self.component_job = {"id": INSTALL_JOB, "status": "RUNNING", "phase": "SERVICE_HOST_TERMINAL", "traceId": INSTALL_JOB}
        self.host_job = None
        self.submits, self.reconciles, self.unhandled = [], [], []
        self.job = None
        self.challenges = {}
        self.accounts = [
            {"id": "mysql-root", "source": "INSTALL", "label": "root 初始密码", "username": "root", "hasPassword": True,
             "version": "root-binding-1", "fullName": "", "email": ""},
            {"id": "mysql-user", "source": "INSTALL", "label": "业务账号密码", "username": "operator", "hasPassword": False,
             "version": "user-binding-1", "fullName": "", "email": ""},
        ]
        self.admin_id = ADMIN
        self.busy_once = False
        self.page = None

    def api(self, route):
        request = route.request
        path = urlsplit(request.url).path.removeprefix("/ops/api/v1")
        if path == "/auth/csrf":
            data = {"csrfToken": "fixture"}
        elif path == "/auth/me":
            data = {"id": self.admin_id, "username": "fixture-admin"}
        elif path == "/hosts":
            data = self.hosts
        elif path == "/status":
            data = {"status": "RUNNING", "uptimeSeconds": 100}
        elif path == "/settings":
            data = {"name": "Ops fixture"}
        elif path in {"/credentials", "/host-observations"}:
            data = []
        elif path == "/audit":
            data = {"items": [], "nextCursor": ""}
        elif path == "/observation-batches":
            data = None
        elif path == "/software-instances":
            data = {"items": self.instances, "total": len(self.instances)}
        elif path == "/software-recipes":
            data = [self.recipe]
        elif path in {"/software-official-recipes", "/installation-materials"}:
            data = []
        elif path == "/plugin-installations":
            data = {"items": [], "total": 0}
        elif path == f"/hosts/{HOST}/software-install-plans":
            plan = request.post_data_json
            assert plan["config"]["runtimeMode"] == "DOCKER" and plan["recipeKey"] == self.recipe["key"]
            data = {**plan, "id": "nginx-plan", "targetId": HOST, "definition": self.recipe, "expiresAt": "2099-01-01T00:00:00Z",
                    "portMappings": [], "checks": [], "paths": {"root": "/opt/sf-qa/nginx", "data": "/opt/sf-qa/nginx/data", "config": "/opt/sf-qa/nginx/conf"}}
        elif path == "/software-install-plans/nginx-plan/submit":
            assert request.post_data_json["idempotencyKey"]
            self.component_submits += 1
            data = {"jobId": INSTALL_JOB, "softwareInstanceId": "nginx-instance"}
        elif path == f"/jobs/{INSTALL_JOB}":
            data = self.component_job
        elif path == f"/hosts/{HOST}/checks":
            data = self.hosts[0]
        elif path == f"/hosts/{HOST}/preparation":
            data = {"hostId": HOST, "expectedHostVersion": "4", "account": "ops", "installRoot": "/opt/sf-qa",
                    "platform": "linux/amd64", "steps": [], "commands": {}, "authorization": {}, "remoteJob": self.host_job,
                    "package": {"available": True, "byteSize": 4096, "fileName": "fixture.tar.gz"}}
        elif path == "/jobs":
            data = {"items": [{**self.job, "kind": "HOST_PREPARE", "subjectName": "主机终端工具",
                               "phase": "TERMINAL_INSTALLING", "createdAt": STAMP}], "nextCursor": ""}
        elif path in {f"/hosts/{host['id']}/terminal-tools" for host in self.hosts}:
            host = next(it for it in self.hosts if it["id"] == path.split("/")[2])
            data = {"hostId": host["id"], "expectedVersion": host["version"], "onlineAvailable": host["kind"] == "SSH",
                    "downloadUrl": "/ops/api/v1" + path + "/download", "job": self.job if host["id"] == HOST else None}
        elif path == f"/hosts/{LOCAL}/terminal-tools/download":
            route.fulfill(body="#!/bin/bash\n# fixture installer", content_type="application/x-sh",
                          headers={"Content-Disposition": "attachment; filename=install-sf-docker.sh"})
            return
        elif path == "/auth/secure-challenge":
            target = request.post_data_json["path"]
            assert target == f"/hosts/{HOST}/terminal-tools/install" or target == ACCOUNT_PATH or target.startswith(ACCOUNT_PATH + "/")
            key = secrets.token_hex(32)
            self.challenges[key] = target
            data = {"challengeId": key, "serverPublicKey": self.public}
        elif path == ACCOUNT_PATH and request.method == "GET":
            data = {"accounts": self.accounts}
        elif path == f"/software-instances/{REDIS}/login-accounts" and request.method == "GET":
            data = {"accounts": []}
        elif path == ACCOUNT_PATH or path.startswith(ACCOUNT_PATH + "/"):
            envelope = request.post_data_json
            assert set(envelope) == {"challengeId", "clientPublicKey", "iv", "ciphertext"}
            assert self.challenges.pop(envelope["challengeId"]) == path
            fields = self.page.evaluate("([envelope, path]) => fixtureDecrypt(envelope, path)", [envelope, path])
            if path.endswith("/copy-password"):
                row = next(row for row in self.accounts if row["id"] == path.split("/")[-2])
                assert row["hasPassword"] and fields["expectedVersion"] == row["version"]
                if fields["password"] != "fixture-admin-confirm":
                    route.fulfill(status=403, json={"code": "REAUTH_FAILED", "msg": "管理员密码不正确"})
                    return
                data = {"password": "fixture-root-secret" if row["source"] == "INSTALL" else "fixture-manual-secret"}
            elif path == ACCOUNT_PATH:
                assert fields["expectedVersion"] == "0" and fields["password"] == "fixture-manual-secret"
                assert all(row["id"] != fields["id"] for row in self.accounts)
                data = {key: fields[key] for key in ["id", "username", "email", "fullName"]}
                data.update(version="1", source="MANUAL", label="", hasPassword=True)
                self.accounts.append(data)
            else:
                data = next(row for row in self.accounts if row["id"] == path.split("/")[-1])
                assert data["source"] == "MANUAL" and fields["expectedVersion"] == "1" and fields["password"] == ""
                data.update(fullName=fields["fullName"], version="2")
        elif path == f"/hosts/{HOST}/terminal-tools/install":
            envelope = request.post_data_json
            assert set(envelope) == {"challengeId", "clientPublicKey", "iv", "ciphertext"}
            assert self.challenges.pop(envelope["challengeId"]) == path
            submitted = self.page.evaluate("([envelope, path]) => fixtureDecrypt(envelope, path)", [envelope, path])
            assert submitted.pop("confirmed") is True
            password = submitted.pop("sudoPassword", None)
            assert password == ("fixture-pass" if not self.submits else None)
            assert submitted["expectedVersion"] == "4"
            if self.busy_once:
                self.busy_once = False
                assert submitted == self.submits[0]
                route.fulfill(status=429, json={"code": "HOST_CHECK_BUSY", "msg": "主机检查已达并发上限"})
                return
            self.submits.append(submitted)
            if len(self.submits) == 1:
                self.hosts[0]["version"] = "5"
                self.job = {"id": JOB, "status": "UNKNOWN", "phase": "VERIFYING", "errorCode": "CONNECTION_LOST",
                            "traceId": JOB, "updatedAt": STAMP, "message": "SSH 连接中断"}
                route.abort("connectionreset")
                return
            assert len(self.submits) == 2 and self.submits[0] == self.submits[1]
            data = {"jobId": JOB}
        elif path == f"/hosts/{HOST}/terminal-tools/reconcile":
            assert request.post_data_json == {"expectedVersion": "5", "jobId": JOB}
            self.reconciles.append(JOB)
            self.job.update(status="SUCCEEDED", errorCode="TERMINAL_TOOLS_READY", message="主机终端工具已安装。")
            data = {"jobId": JOB}
        else:
            self.unhandled.append((request.method, path))
            route.fulfill(status=404, json={"code": "UNMOCKED", "msg": f"Unmocked {path}"})
            return
        route.fulfill(json={"code": "SUCCESS", "data": data})


def run(chromium):
    assert (DIST / "index.html").exists(), f"Build web/dist first: {DIST}"
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(StaticHandler, directory=str(DIST)))
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=chromium)
            context = browser.new_context(permissions=["clipboard-read", "clipboard-write"], viewport={"width": 1440, "height": 900})
            page = context.new_page()
            page.set_default_timeout(6500)
            fixture = Fixture()
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.route("**/ops/api/v1/**", fixture.api)
            page.goto(f"http://127.0.0.1:{server.server_port}/")
            fixture.page = page
            fixture.public = page.evaluate("""async () => {
                const curve = {name:'ECDH', namedCurve:'P-256'}, enc = new TextEncoder();
                const pair = await crypto.subtle.generateKey(curve,true,['deriveBits']);
                const bytes = s => Uint8Array.from(atob(s.replaceAll('-','+').replaceAll('_','/')),c=>c.charCodeAt(0));
                window.fixtureDecrypt = async (e,path) => {
                    const peer = await crypto.subtle.importKey('raw',bytes(e.clientPublicKey),curve,false,[]);
                    const shared = await crypto.subtle.deriveBits({name:'ECDH',public:peer},pair.privateKey,256);
                    const imported = await crypto.subtle.importKey('raw',shared,'HKDF',false,['deriveKey']);
                    const info='streamfusion-ops-sensitive-v1';
                    const key = await crypto.subtle.deriveKey({name:'HKDF',hash:'SHA-256',
                        salt:enc.encode(e.challengeId),info:enc.encode(info)},imported,{name:'AES-GCM',length:256},false,['decrypt']);
                    const aad = enc.encode([info,'POST',path,e.challengeId].join(String.fromCharCode(10)));
                    const plain = await crypto.subtle.decrypt({name:'AES-GCM',iv:bytes(e.iv),additionalData:aad},key,bytes(e.ciphertext));
                    return JSON.parse(new TextDecoder().decode(plain));
                };
                const pub = new Uint8Array(await crypto.subtle.exportKey('raw',pair.publicKey));
                return btoa(String.fromCharCode(...pub)).replaceAll('+','-').replaceAll('/','_').replaceAll('=','');
            }""")
            page.get_by_role("button", name="软件管理", exact=True).click()

            def open_detail(name):
                card = page.get_by_role("article", name=name, exact=True)
                card.get_by_role("button", name="详情", exact=True).click()
                dialog = page.get_by_role("dialog", name=name, exact=True)
                expect(dialog).to_be_visible()
                return dialog

            def close_detail(dialog):
                dialog.get_by_role("button", name="Close this dialog").click()
                expect(dialog).not_to_be_visible()

            def copy_and_check(dialog, label, command):
                entry = dialog.locator(".command-entry").filter(has=page.get_by_text(label, exact=True))
                expect(entry.locator("code")).to_have_text(command)
                entry.get_by_role("button", name=f"复制{label}命令", exact=True).click()
                page.wait_for_function("command => navigator.clipboard.readText().then(text => text === command)", arg=command)

            redis_card = page.get_by_role("article", name="旧版 Redis", exact=True)
            expect(redis_card.locator(".card-version")).to_have_text("版本未确认")
            expect(redis_card.locator(".card-version")).to_have_attribute("title", "版本未确认")
            redis = open_detail("旧版 Redis")
            expect(redis.get_by_role("heading", name="终端命令", exact=True)).to_be_visible()
            expect(redis.locator(".software-facts").filter(has=page.get_by_text("所在主机", exact=True))).to_contain_text("版本未确认")
            expect(redis.get_by_text("Redis server v=7.2.4", exact=True)).to_be_visible()
            expect(redis.get_by_text("镜像引用", exact=True)).to_have_count(0)
            expect(redis.get_by_text("sha256:" + "c" * 64, exact=True)).to_have_count(0)
            terminal_bottom = redis.locator(".terminal-commands").bounding_box()
            host_top = redis.get_by_text("所在主机", exact=True).bounding_box()
            assert host_top["y"] - (terminal_bottom["y"] + terminal_bottom["height"]) >= 22
            copy_and_check(redis, "容器内连接", "docker exec -it sf-redis redis-cli --askpass")
            copy_and_check(redis, "宿主机 TCP 连接", "redis-cli -h 127.0.0.1 -p 16379 --askpass")
            expect(redis.get_by_role("button", name="补装宿主机客户端")).to_have_count(0)
            redis.get_by_role("tab", name="登录账号", exact=True).click()
            expect(redis.get_by_text("暂无登录账号", exact=True)).to_be_visible()
            expect(redis.get_by_role("button", name="添加登录账号")).to_be_enabled()
            close_detail(redis)

            native = open_detail("原生 Redis")
            copy_and_check(native, "宿主机 TCP 连接", "/srv/store/cache/bin/redis-cli -h 127.0.0.1 -p 26379 --askpass")
            expect(native.get_by_role("button", name="补装宿主机客户端")).to_have_count(0)
            close_detail(native)

            mysql = open_detail("旧版 MySQL")
            copy_and_check(mysql, "容器内连接", "docker exec -it sf-mysql mysql --protocol=SOCKET --user=operator --password --database=warehouse")
            copy_and_check(mysql, "宿主机 TCP 连接", "mysql --protocol=TCP --host=127.0.0.1 --port=13306 --user=operator --password --database=warehouse")
            mysql.get_by_role("tab", name="登录账号", exact=True).click()
            accounts = mysql.get_by_role("region", name="软件登录账号")
            root = accounts.get_by_role("listitem").filter(has=page.get_by_text("root", exact=True))
            expect(root).to_contain_text("密码已保存")
            expect(root.get_by_role("button", name="编辑")).to_have_count(0)
            unavailable = accounts.get_by_role("listitem").filter(has=page.get_by_text("operator", exact=True))
            expect(unavailable.get_by_role("button", name="复制密码")).to_be_disabled()
            root.get_by_role("button", name="复制密码").click()
            confirmation = page.get_by_role("dialog", name="复制软件登录密码")
            previous_clipboard = page.evaluate("() => navigator.clipboard.readText()")
            confirmation.get_by_label("Ops 管理员登录密码").fill("fixture-wrong-confirmation")
            confirmation.get_by_role("button", name="验证并复制").click()
            expect(confirmation.get_by_role("alert")).to_have_text("管理员密码不正确")
            assert page.evaluate("() => navigator.clipboard.readText()") == previous_clipboard
            confirmation.get_by_label("Ops 管理员登录密码").fill("fixture-admin-confirm")
            confirmation.get_by_role("button", name="验证并复制").click()
            expect(confirmation).not_to_be_visible()
            assert page.evaluate("() => navigator.clipboard.readText()") == "fixture-root-secret"
            expect(accounts).not_to_contain_text("fixture-root-secret")
            for username in ["backup", "analyst"]:
                accounts.get_by_role("button", name="添加登录账号").click()
                accounts.get_by_label("用户名", exact=True).fill(username)
                accounts.get_by_label("密码", exact=True).fill("fixture-manual-secret")
                accounts.get_by_role("button", name="保存登录账号").click()
                expect(accounts.get_by_text(username, exact=True)).to_be_visible()
            backup = accounts.get_by_role("listitem").filter(has=page.get_by_text("backup", exact=True))
            backup.get_by_role("button", name="编辑").click()
            accounts.get_by_label("全名", exact=True).fill("备份账号")
            mysql.get_by_role("tab", name="软件概览").click()
            page.get_by_role("button", name="继续编辑").click()
            expect(accounts.get_by_label("全名", exact=True)).to_have_value("备份账号")
            accounts.get_by_role("button", name="保存登录账号").click()
            expect(backup).to_contain_text("备份账号")
            expect(accounts.get_by_role("listitem")).to_have_count(4)
            backup.get_by_role("button", name="复制密码").click()
            confirmation.get_by_label("Ops 管理员登录密码").fill("fixture-admin-confirm")
            confirmation.get_by_role("button", name="验证并复制").click()
            expect(confirmation).not_to_be_visible()
            assert page.evaluate("() => navigator.clipboard.readText()") == "fixture-manual-secret"
            close_detail(mysql)
            pg = open_detail("已登记 PostgreSQL")
            page.set_viewport_size({"width": 390, "height": 844})
            pre = pg.locator(".command-entry").filter(has=page.get_by_text("宿主机 TCP 连接", exact=True)).locator("pre")
            expect(pre).to_be_visible()
            assert pre.evaluate("(block) => block.scrollWidth > block.clientWidth && getComputedStyle(block).overflowX === 'auto'")
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
            drawer_body = pre.locator("xpath=ancestor::*[contains(@class, 'el-drawer__body')][1]")
            assert drawer_body.evaluate("(body) => body.scrollWidth <= body.clientWidth + 1")
            copy_and_check(pg, "宿主机 TCP 连接", "read -r -p 'PostgreSQL 用户名：' sf_pg_user && "
                           "read -r -p '数据库名：' sf_pg_db && psql -h 127.0.0.1 -p 15432 -U \"$sf_pg_user\" -d \"$sf_pg_db\" -W")
            close_detail(pg)
            page.set_viewport_size({"width": 1440, "height": 900})
            maria = open_detail("已登记 MariaDB")
            copy_and_check(maria, "宿主机 TCP 连接", "read -r -p 'MySQL 用户名：' sf_user && [ -n \"$sf_user\" ] && "
                           "mysql --protocol=TCP --host=127.0.0.1 --port=13308 --user=\"$sf_user\" --password")
            close_detail(maria)
            expect(page.get_by_role("button", name="统一终端工具", exact=True)).to_have_count(0)
            expect(page.get_by_role("button", name="终端工具维护", exact=True)).to_have_count(0)
            page.get_by_role("button", name="软件安装", exact=True).click()
            card = page.locator("article.plugin-card").filter(has=page.get_by_role("heading", name="Nginx", exact=True))
            card.get_by_role("button", name="安装", exact=True).click()
            install = page.locator(".service-install-dialog.el-dialog:visible").last
            install.get_by_role("button", name="检查安装条件", exact=True).click()
            expect(install.get_by_label("预检确认")).to_contain_text("缺少的客户端会从主机的 APT 软件源补装")
            install.get_by_role("button", name="确认安装", exact=True).click()
            expect(install.get_by_label("执行结果")).to_contain_text("准备宿主机终端命令")
            fixture.component_job.update(status="SUCCEEDED", phase="SERVICE_INSTALLED")
            expect(install.get_by_label("执行结果")).to_contain_text("服务检查通过")
            assert fixture.component_submits == 1 and not fixture.submits
            install.get_by_role("button", name="关闭", exact=True).click()

            page.get_by_role("button", name="主机管理", exact=True).click()
            page.get_by_role("row").filter(has_text="fixture-ubuntu").get_by_role("button", name="详情", exact=True).click()
            host_detail = page.get_by_role("dialog", name="fixture-ubuntu", exact=True)
            host_detail.get_by_role("tab", name="安装准备", exact=True).click()
            expect(host_detail).to_contain_text("已有 Docker 实例也可使用")
            host_detail.get_by_role("button", name="离线", exact=True).click()
            expect(host_detail).to_contain_text("已有 Docker 实例也可使用")
            fixture.host_job = {"id": "old-preparation", "status": "SUCCEEDED", "phase": "VERIFYING_EXECUTOR",
                                "errorCode": "PREPARATION_READY", "message": "服务器接入成功", "updatedAt": STAMP}
            host_detail.get_by_role("button", name="重新加载", exact=True).click()
            expect(host_detail).to_contain_text("更新接入组件即可在 Ubuntu 24.04 上一次补齐")
            fixture.host_job = {**fixture.host_job, "id": "current-preparation", "includesHostTerminal": True}
            host_detail.get_by_role("button", name="重新加载", exact=True).click()
            expect(host_detail).not_to_contain_text("更新接入组件即可在 Ubuntu 24.04 上一次补齐")
            fixture.host_job.update(status="FAILED", errorCode="TERMINAL_TOOLS_APT_UPDATE", message="检查主机软件源")
            host_detail.get_by_role("button", name="重新加载", exact=True).click()
            expect(host_detail.get_by_role("heading", name="宿主机终端工具准备失败", exact=True)).to_be_visible()
            expect(host_detail).to_contain_text("检查主机软件源")
            page.keyboard.press("Escape")
            expect(host_detail).not_to_be_visible()

            def open_tools(host):
                page.get_by_role("row").filter(has_text=host).get_by_role("button", name="详情", exact=True).click()
                detail = page.get_by_role("dialog", name=host, exact=True)
                detail.get_by_role("button", name="终端工具维护", exact=True).click()
                tools = page.get_by_role("dialog", name=f"{host} · 终端工具维护", exact=True)
                expect(tools).to_be_visible()
                return tools

            def close_tools(tools):
                close_detail(tools)
                page.keyboard.press("Escape")
                expect(page.get_by_role("dialog")).to_have_count(0)

            tools = open_tools("fixture-local")
            expect(tools.get_by_role("button", name="检查并修复工具")).to_have_count(0)
            copy_and_check(tools, "在主机手动修复", "sudo bash ./install-sf-docker.sh")
            with page.expect_download() as download:
                tools.get_by_role("link", name="下载安装脚本").click()
            assert download.value.suggested_filename == "install-sf-docker.sh"
            close_tools(tools)
            tools = open_tools("fixture-ubuntu")
            copy_and_check(tools, "查看本机容器", "sf-docker list")
            copy_and_check(tools, "查看终端命令用法", "sf-docker --help")
            tools.get_by_role("button", name="检查并修复工具").click()
            tools.get_by_role("button", name="取消", exact=True).click()
            assert not fixture.submits
            tools.get_by_role("button", name="检查并修复工具").click()
            tools.get_by_label("sudo 密码", exact=False).fill("fixture-pass")
            tools.get_by_role("button", name="确认修复").click()
            expect(tools.get_by_role("button", name="核对原提交")).to_be_visible()
            expect(tools.get_by_role("button", name="核对原作业")).to_have_count(0)
            close_tools(tools)
            tools = open_tools("fixture-local")
            expect(tools.get_by_role("button", name="核对原提交")).to_have_count(0)
            expect(tools.get_by_text("SSH 连接中断")).to_have_count(0)
            close_tools(tools)
            fixture.admin_id = "44444444-4444-4444-8444-444444444442"
            tools = open_tools("fixture-ubuntu")
            expect(tools.get_by_text("SSH 连接中断")).to_be_visible()
            expect(tools.get_by_role("button", name="核对原提交")).to_have_count(0)
            expect(tools.get_by_text("CONNECTION_LOST", exact=True)).to_be_hidden()
            tools.get_by_text("诊断信息", exact=True).click()
            expect(tools.get_by_text("CONNECTION_LOST", exact=True)).to_be_visible()
            expect(tools.get_by_text(JOB, exact=True)).to_be_visible()
            close_tools(tools)
            fixture.admin_id = ADMIN
            tools = open_tools("fixture-ubuntu")
            fixture.busy_once = True
            tools.get_by_role("button", name="核对原提交").click()
            expect(tools).to_contain_text("主机检查已达并发上限")
            tools.get_by_role("button", name="核对原提交").click()
            expect(tools.get_by_role("button", name="核对原提交")).to_have_count(0)
            tools.get_by_role("button", name="核对原作业").click()
            expect(tools).to_contain_text("主机终端工具已安装。")
            expect(tools).to_contain_text("最近一次手动维护 · 已完成")
            expect(tools).not_to_contain_text("TERMINAL_TOOLS_READY")
            expect(tools).not_to_contain_text(JOB)
            assert len(fixture.submits) == 2 and fixture.submits[0] == fixture.submits[1] and fixture.reconciles == [JOB]
            close_tools(tools)
            fixture.instances.clear()
            tools = open_tools("fixture-ubuntu")
            expect(tools).to_contain_text("主机终端工具已安装。")
            expect(tools).not_to_contain_text("TERMINAL_TOOLS_READY")
            close_tools(tools)
            page.get_by_role("button", name="发布作业", exact=True).click()
            job_row = page.get_by_role("row").filter(has_text="主机终端工具")
            expect(job_row).to_contain_text("安装主机终端工具")
            assert not errors and not fixture.unhandled, (errors, fixture.unhandled)
            print("PASS: Docker installation, instance commands, login account creation and editing, verified password copy, host repair and download.")
            browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--browser-executable", help="Path to an existing Chromium binary")
    run(parser.parse_args().browser_executable)
