check_owner
if [[ "$OPS_RUNTIME_MODE" == DOCKER ]]; then
  [[ "$(docker inspect --format '{{index .Config.Labels "streamfusion.ops.software-id"}}|{{.Image}}|{{.State.Running}}' "$container")" == "$OPS_SOFTWARE_ID|@@IMAGE_ID@@|true" ]] || fail '容器身份或运行状态不符'
  docker exec "$container" /label-studio/.venv/bin/python -c 'import importlib.metadata; assert importlib.metadata.version("label-studio")=="@@VERSION@@"'
  [[ "$(docker exec "$container" /bin/sh -c 'printf "%s" "$DATA_UPLOAD_MAX_MEMORY_SIZE"')" == "$((OPS_PARAM_MAX_UPLOAD_MIB*1024*1024))" ]] || fail '上传大小配置未生效'
else
  systemctl is-active --quiet "$unit" || fail '服务尚未运行'
  "$root/runtime/venv/bin/python" -c 'import importlib.metadata; assert importlib.metadata.version("label-studio")=="@@VERSION@@"'
  grep -Fxq "export DATA_UPLOAD_MAX_MEMORY_SIZE=$((OPS_PARAM_MAX_UPLOAD_MIB*1024*1024))" "$root/config/runtime.env" || fail '上传大小配置不符'
fi
address=$OPS_BIND_ADDRESS
ready=false
for attempt in $(seq 1 90); do
  status=$(curl --silent --output /dev/null --write-out '%{http_code}' --connect-timeout 2 --max-time 5 "http://$address:$OPS_HOST_PORT/health/" || true)
  if [[ "$status" == 200 ]]; then ready=true; break; fi
  sleep 2
done
[[ "$ready" == true ]] || fail 'Label Studio 健康检查未通过，请查看启动输出'
printf '%s\n' 'Label Studio @@VERSION@@ 已就绪'
