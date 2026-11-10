check_owner
python=$(cat "$root/config/python-binary")
"$python" -I - "$root/config/ytlabel.json" <<'PY'
import json, os, sys
with open(sys.argv[1], encoding='utf8') as source:
    settings = json.load(source)
if settings['host'] != os.environ['OPS_PARAM_BIND_ADDRESS'] or settings['port'] != int(os.environ['OPS_PARAM_WEB_PORT']):
    sys.exit('访问配置与本次操作不一致')
PY
systemctl is-active --quiet "$unit" || fail '服务尚未运行'
host="$OPS_PARAM_BIND_ADDRESS"
[[ "$host" != 0.0.0.0 ]] || host=127.0.0.1
curl --fail --silent --max-time 3 "http://$host:$OPS_PARAM_WEB_PORT/" | grep -qi 'ytlabel' || fail '服务页面未就绪'
printf '%s\n' '访问配置与服务页面验证通过'
