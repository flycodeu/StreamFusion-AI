account_name() {
  [[ "$OPS_PARAM_USERNAME" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31}$ && "${OPS_PARAM_USERNAME,,}" != root ]] || { echo '请使用普通业务账号'; exit 1; }
  [[ "$OPS_PARAM_HOST" =~ ^[a-zA-Z0-9_.:%/-]{1,255}$ ]] || { echo '账号来源地址格式无效'; exit 1; }
  user_sql=$(literal "$OPS_PARAM_USERNAME"); host_sql=$(literal "$OPS_PARAM_HOST")
}
