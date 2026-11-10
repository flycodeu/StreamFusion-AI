check_owner
printf '软件：%s\n版本：%s\n' "$app" "$version"
if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
  owned_container
  docker inspect --format '运行状态：{{.State.Status}}\n镜像：{{.Image}}\n启动时间：{{.State.StartedAt}}' "$container"
else
  [[ "$app" != gitlab ]] || unit=gitlab-runsvdir.service
  systemctl show --property=ActiveState,SubState,MainPID,MemoryCurrent -- "$unit"
fi
printf '\n配置文件：\n'
find "$root/config" -maxdepth 1 -type f -printf '%f\n' | grep -Ev 'password|pgpass|nacos.env|ops-owner|repository' || true
printf '\n数据与日志空间：\n'
du -sh -- "$root/data" "$root/logs"
