check_owner
check_no_mounts "$root/runtime"
if [[ "$app" == gitlab ]]; then
  [[ -f "$root/config/system-paths-owned" ]] || fail '缺少 GitLab 目录归属记录'
  check_owned_package gitlab-ce
  /opt/gitlab/bin/gitlab-ctl stop
  remove_owned_package gitlab-ce
else
  if [[ "$app" == postgresql ]]; then
    if command -v pg_lsclusters >/dev/null; then
      [[ -z "$(pg_lsclusters --no-header | awk '$1==18 {print $0}')" ]] || fail '存在另外的 PostgreSQL 18 集群，请先迁移后卸载系统包'
    fi
    check_owned_package postgresql-18
  fi
  path="/etc/systemd/system/$unit"
  [[ -f "$path" && ! -L "$path" ]] || fail '服务单元归属不符'
  grep -Fq "Description=Ops $app $OPS_SOFTWARE_ID" "$path" || fail '服务单元已被外部修改'
  systemctl disable --now "$unit"
  rm -- "$path"
  systemctl daemon-reload
  if [[ "$app" == postgresql ]]; then remove_owned_package postgresql-18; fi
fi
rm -rf -- "$root/runtime" "$root/bin/run"
printf '%s\n' '服务已卸载，数据与配置已保留'
