check_owner
for attempt in {1..60}; do
  if systemctl is-active --quiet "$unit" && curl --fail --silent --max-time 3 "http://$OPS_BIND_ADDRESS:$OPS_HOST_PORT/" | grep -qi 'ytlabel'; then
    printf '%s\n' 'ytlabel 4.0 已启动，上传限制和只读素材目录已写入本实例配置'
    exit 0
  fi
  sleep 2
done
fail 'ytlabel 未就绪，请查看运行日志；核对 Python 版本、系统库和素材目录读取权限'
