check_owner
config="$root/config/ytlabel.json"
backup="$root/config/ytlabel-network.previous.json"
[[ -f "$config" && ! -L "$config" ]] || fail '缺少本实例访问配置'
[[ ! -L "$backup" && ( ! -e "$backup" || -f "$backup" ) ]] || fail '配置备份位置异常'
grep -Fxq "Description=Ops ytlabel $OPS_SOFTWARE_ID" "/etc/systemd/system/$unit" || fail '服务单元归属不符'
python=$(cat "$root/config/python-binary")
"$python" -I - "$config" <<'PY'
import ipaddress, json, os, stat, sys, tempfile
config = sys.argv[1]
address = ipaddress.IPv4Address(os.environ['OPS_PARAM_BIND_ADDRESS'])
port = int(os.environ['OPS_PARAM_WEB_PORT'])
if address.is_multicast or not 1 <= port <= 65535:
    sys.exit('监听地址或端口无效')
metadata = os.stat(config, follow_symlinks=False)
if not stat.S_ISREG(metadata.st_mode) or metadata.st_size > 65536:
    sys.exit('访问配置不可读取')
with open(config, encoding='utf8') as source:
    settings = json.load(source)
if not isinstance(settings, dict) or not {'host', 'port', 'maxUploadMiB'} <= settings.keys():
    sys.exit('原访问配置不完整')
settings.update(host=str(address), port=port)
with tempfile.NamedTemporaryFile(mode='w', dir=os.path.dirname(config), prefix='.ytlabel-network-', delete=False, encoding='utf8') as output:
    temporary = output.name
    json.dump(settings, output)
os.chown(temporary, metadata.st_uid, metadata.st_gid)
os.chmod(temporary, stat.S_IMODE(metadata.st_mode))
os.replace(temporary, config + '.next')
PY
cp -p -- "$config" "$backup"
rollback() {
  trap - ERR
  cp -p -- "$backup" "$config"
  systemctl restart "$unit" || true
  fail '访问配置未生效，已恢复原配置，请查看运行日志'
}
trap rollback ERR
mv -- "$config.next" "$config"
systemctl restart "$unit"
host="$OPS_PARAM_BIND_ADDRESS"
[[ "$host" != 0.0.0.0 ]] || host=127.0.0.1
ready=false
for attempt in {1..30}; do
  if systemctl is-active --quiet "$unit" && curl --fail --silent --max-time 2 "http://$host:$OPS_PARAM_WEB_PORT/" | grep -qi 'ytlabel'; then ready=true; break; fi
  sleep 2
done
[[ "$ready" == true ]] || rollback
trap - ERR
printf '访问配置已生效：%s:%s\n' "$OPS_PARAM_BIND_ADDRESS" "$OPS_PARAM_WEB_PORT"
