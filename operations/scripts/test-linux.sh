#!/usr/bin/env bash
# Root integration test; all writable paths, unit and account are unique to this run.
set -Eeuo pipefail
[[ $EUID == 0 && $# == 1 ]] || { echo '用法: sudo bash scripts/test-linux.sh /absolute/path/to/binary'; exit 2; }
scripts=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
binary=$(realpath "$1")
suffix=$$
name="sfo-i01-$suffix"
prefix="/opt/$name"
state="/var/lib/$name"
work=$(mktemp -d /tmp/sfo-i01-XXXXXX)
[[ ! -e $prefix && ! -e $state && ! -e /etc/systemd/system/$name.service ]] || exit 1
cp "$binary" "$work/streamfusion-ops"; chmod 0755 "$work/streamfusion-ops"
sha=$(sha256sum "$work/streamfusion-ops" | cut -d' ' -f1)
python3 -c 'import secrets,sys; from pathlib import Path; Path(sys.argv[1]).write_text("I01-"+secrets.token_hex(16))' "$work/password"
chmod 0600 "$work/password"
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
echo "隔离测试: $name"
cleanup() {
  systemctl disable --now "$name.service" >/dev/null 2>&1 || true
  rm -f -- "/etc/systemd/system/$name.service"
  systemctl daemon-reload
  # 固定本轮创建的绝对目录，拒绝任何意外改写或符号链接。
  [[ $prefix == "/opt/sfo-i01-$suffix" && ! -L $prefix ]] && rm -rf -- "$prefix"
  [[ $state == "/var/lib/sfo-i01-$suffix" && ! -L $state ]] && rm -rf -- "$state"
  [[ $work == /tmp/sfo-i01-* && ! -L $work ]] && rm -rf -- "$work"
  userdel "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
args=(--binary "$work/streamfusion-ops" --sha256 "$sha" --prefix "$prefix" --state "$state" --name "$name" --port "$port")
bash "$scripts/install-linux.sh" "${args[@]}"
[[ ! -e $prefix ]] || { echo 'plan performed writes'; exit 1; }
if bash "$scripts/install-linux.sh" "${args[@]/$sha/0000000000000000000000000000000000000000000000000000000000000000}" --apply >/dev/null 2>&1; then echo 'bad checksum accepted'; exit 1; fi
# 在初始化密码已暂存但事务尚未完成时中断整个安装进程组，再按同一计划续装。
python3 - "$scripts/install-linux.sh" "${args[@]}" --apply --password-file "$work/password" <<'PY'
import os, pathlib, signal, subprocess, sys, time
params=sys.argv[1:]
state=pathlib.Path(params[params.index('--state')+1])
process=subprocess.Popen(['bash',*params],start_new_session=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
deadline=time.monotonic()+15
while time.monotonic()<deadline and process.poll() is None:
    if state.exists() and list(state.glob('.password-*')):
        os.killpg(process.pid,signal.SIGTERM)
        process.communicate(timeout=10)
        assert process.returncode != 0
        print('PASS: interrupted during initialization')
        break
    time.sleep(0.005)
else:
    process.terminate()
    process.communicate(timeout=10)
    raise AssertionError('did not reach interruption point')
PY
bash "$scripts/install-linux.sh" "${args[@]}" --apply --password-file "$work/password"
bash "$scripts/install-linux.sh" "${args[@]}" --apply
python3 - "$name.service" <<'PY'
import json, os, pathlib, subprocess, sys, time
pid=int(subprocess.check_output(['systemctl','show','-p','MainPID','--value',sys.argv[1]],text=True))
def ticks():
    # 字段14/15为本进程user/system CPU；先剥离括号中的进程名。
    fields=pathlib.Path(f'/proc/{pid}/stat').read_text().rsplit(')',1)[1].split()
    return int(fields[11])+int(fields[12])
start=ticks(); time.sleep(5); elapsed_cpu=(ticks()-start)/os.sysconf('SC_CLK_TCK')
rss_line=next(x for x in pathlib.Path(f'/proc/{pid}/status').read_text().splitlines() if x.startswith('VmRSS:'))
rss_mib=int(rss_line.split()[1])/1024
cpu_percent=elapsed_cpu/5*100
assert rss_mib<=150 and cpu_percent<2, (rss_mib,cpu_percent)
print(json.dumps({'scenario':'I01 idle / one controller / no targets','sampleSeconds':5,'rssMiB':round(rss_mib,2),'cpuOneCorePercent':round(cpu_percent,2)}))
PY
python3 "$scripts/test-runtime.py" --url "http://127.0.0.1:$port" --binary "$prefix/streamfusion-ops" --state "$state" --password-file "$work/password" --output "$work/recovery" --unit "$name.service"
hash_before=$(sha256sum "$state/controller.sqlite")
bash "$scripts/uninstall-linux.sh" --prefix "$prefix" --apply
[[ -f $state/master.key && -f $state/controller.sqlite && ! -e $prefix/streamfusion-ops && $hash_before == "$(sha256sum "$state/controller.sqlite")" ]]
echo 'PASS: install / repeat-install / restart / recovery / uninstall-keeps-state'
