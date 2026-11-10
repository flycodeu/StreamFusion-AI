check_owner
if [[ "$OPS_RUNTIME_MODE" == DOCKER ]]; then owned_container; fi
for attempt in {1..120}; do
  ready=false
  case "$app" in
    postgresql)
      if [[ "$OPS_RUNTIME_MODE" == DOCKER ]]; then docker exec "$container" pg_isready -U "$OPS_PARAM_DB_USER" -d "$OPS_PARAM_DB_NAME" >/dev/null 2>&1 && ready=true; else /usr/lib/postgresql/18/bin/pg_isready -h "$root/data/run" -p "$OPS_HOST_PORT" >/dev/null 2>&1 && ready=true; fi
      ;;
    prometheus) curl --fail --silent --max-time 5 "http://$OPS_BIND_ADDRESS:$OPS_HOST_PORT/-/ready" >/dev/null && ready=true ;;
    jenkins)
      code="$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 5 "http://$OPS_BIND_ADDRESS:$OPS_HOST_PORT/login" || true)"
      [[ "$code" == 200 || "$code" == 403 ]] && ready=true
      ;;
    gitlab) curl --fail --silent --max-time 5 "http://$OPS_BIND_ADDRESS:$OPS_HOST_PORT/-/readiness" >/dev/null && ready=true ;;
    nacos) curl --fail --silent --max-time 5 "http://$OPS_BIND_ADDRESS:$OPS_HOST_PORT/nacos/v3/admin/core/state/readiness" >/dev/null && curl --fail --silent --max-time 5 "http://$OPS_BIND_ADDRESS:$OPS_PORT_CONSOLE/v3/console/health/readiness" >/dev/null && ready=true ;;
  esac
  if [[ "$ready" == true ]]; then
    if [[ "$OPS_RUNTIME_MODE" == DOCKER ]]; then [[ "$(docker inspect --format '{{.State.Running}}' "$container")" == true ]] || fail '容器未运行'; else systemctl is-active --quiet "$([[ "$app" == gitlab ]] && echo gitlab-runsvdir.service || echo "$unit")" || fail '服务未运行'; fi
    printf '%s\n' "$app $version 已启动并通过健康检查"
    exit 0
  fi
  sleep 5
done
fail '服务尚未就绪，请查看本次执行日志与运行日志；保留当前任务进行核对'
