[[ "$OPS_PARAM_MAX_CONNECTIONS" =~ ^[0-9]+$ && "$OPS_PARAM_MAX_CONNECTIONS" -ge 10 && "$OPS_PARAM_MAX_CONNECTIONS" -le 10000 ]] || exit 1
[[ "$OPS_PARAM_LONG_QUERY_TIME" =~ ^[0-9]+$ && "$OPS_PARAM_LONG_QUERY_TIME" -le 3600 ]] || exit 1
case "$OPS_PARAM_SLOW_QUERY_LOG" in true) slow=1;; false) slow=0;; *) exit 1;; esac
case "$OPS_PARAM_STORAGE_ENGINE" in InnoDB|MyISAM) engine=$OPS_PARAM_STORAGE_ENGINE;; *) exit 1;; esac
query <<SQL
SET PERSIST max_connections=$OPS_PARAM_MAX_CONNECTIONS;
SET PERSIST slow_query_log=$slow;
SET PERSIST long_query_time=$OPS_PARAM_LONG_QUERY_TIME;
SET PERSIST default_storage_engine='$engine';
SQL
printf '配置已保存并立即生效\n'
