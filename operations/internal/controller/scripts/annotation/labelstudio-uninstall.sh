check_owner
if [[ "$OPS_RUNTIME_MODE" == DOCKER ]]; then
  [[ "$(docker inspect --format '{{index .Config.Labels "streamfusion.ops.software-id"}}' "$container")" == "$OPS_SOFTWARE_ID" ]] || fail '容器归属不符'
  docker stop --time 60 "$container"
  docker rm "$container"
else
  path="/etc/systemd/system/$unit"
  [[ -f "$path" && ! -L "$path" ]] && grep -Fxq "Description=Ops Label Studio $OPS_SOFTWARE_ID" "$path" || fail '服务单元归属不符'
  check_no_mounts "$root/runtime"
  systemctl disable --now "$unit"
  rm -f -- "$path"
  systemctl daemon-reload
  rm -rf -- "$root/runtime"
  rm -f -- "$root/bin/run"
fi
printf '%s\n' 'Label Studio 已卸载，标注数据和配置已保留'
