check_owner
for path in "$root/data" "$root/config" "$root/logs"; do check_no_mounts "$path"; done
if [[ "$app" == gitlab && "$OPS_RUNTIME_MODE" == NATIVE ]]; then
  [[ -f "$root/config/system-paths-owned" ]] || fail '缺少 GitLab 目录归属记录'
  for path in /etc/gitlab /var/opt/gitlab /var/log/gitlab; do
    [[ ! -L "$path" ]] || fail "目录已被替换为链接：$path"
    if [[ -e "$path" ]]; then
      [[ -d "$path" ]] || fail "目录类型已变化：$path"
      grep -Fxq "$path $(stat -c '%d:%i' "$path")" "$root/config/system-paths-owned" || fail "目录已被外部替换：$path"
      check_no_mounts "$path"
    fi
  done
  rm -rf -- /etc/gitlab /var/opt/gitlab /var/log/gitlab
fi
rm -rf -- "$root/data" "$root/config" "$root/logs"
printf '%s\n' '本次数据与配置已删除'
