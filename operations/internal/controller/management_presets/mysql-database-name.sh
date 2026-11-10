database_name() {
  [[ "$OPS_PARAM_DATABASE" =~ ^[a-zA-Z][a-zA-Z0-9_]{0,63}$ ]] || { echo '数据库名称格式无效'; exit 1; }
  case "${OPS_PARAM_DATABASE,,}" in mysql|sys|information_schema|performance_schema) echo '请使用业务数据库'; exit 1;; esac
}
