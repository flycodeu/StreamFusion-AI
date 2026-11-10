set -euo pipefail
umask 077
[[ "$OPS_JOB_ID" =~ ^[a-f0-9-]{36}$ ]] || exit 1
opts=$(mktemp "$OPS_INSTALL_ROOT/work/.mysql-options.XXXXXX")
remote="/tmp/sfops-mysql-$OPS_JOB_ID.cnf"
cleanup() {
  rm -f -- "$opts"
  if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then docker exec "$OPS_SOFTWARE_LOCATOR" rm -f -- "$remote" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
  docker cp "$OPS_SOFTWARE_LOCATOR:/run/secrets/mysql-health.cnf" "$opts" >/dev/null
else
  @NATIVE_OPTIONS@
fi
if [[ -n "${OPS_SECRET_ADMIN_PASSWORD_FILE:-}" ]]; then
  [[ "$(wc -l < "$OPS_SECRET_ADMIN_PASSWORD_FILE")" == 0 ]] || exit 1
  password=$(cat "$OPS_SECRET_ADMIN_PASSWORD_FILE")
  [[ "$password" != *$'\n'* && "$password" != *$'\r'* ]] || exit 1
  password=${password//\\/\\\\}; password=${password//\"/\\\"}
  [[ "$OPS_PARAM_ADMIN_USER" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31}$ ]] || exit 1
  printf '\n[client]\nuser="%s"\npassword="%s"\n' "$OPS_PARAM_ADMIN_USER" "$password" >> "$opts"
  unset password
else
  [[ "$OPS_PARAM_ADMIN_USER" == root ]] || { echo '使用其他管理账号时请选择密码'; exit 1; }
fi
if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then docker cp "$opts" "$OPS_SOFTWARE_LOCATOR:$remote" >/dev/null; fi
query() {
  if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
    docker exec -i "$OPS_SOFTWARE_LOCATOR" mysql "--defaults-extra-file=$remote" --batch --raw --skip-column-names --default-character-set=utf8mb4 --connect-timeout=10
  else
    "@NATIVE_MYSQL@" "--defaults-extra-file=$opts" --batch --raw --skip-column-names --default-character-set=utf8mb4 --connect-timeout=10
  fi
}
