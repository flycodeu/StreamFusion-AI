case "$OPS_PARAM_SLOW_QUERY_LOG" in true) slow=1;; false) slow=0;; *) exit 1;; esac
[[ "$OPS_PARAM_MAX_CONNECTIONS" =~ ^[0-9]+$ && "$OPS_PARAM_LONG_QUERY_TIME" =~ ^[0-9]+$ ]] || exit 1
case "$OPS_PARAM_STORAGE_ENGINE" in InnoDB|MyISAM) ;; *) exit 1;; esac
result=$(query <<SQL
SELECT (@@global.max_connections=$OPS_PARAM_MAX_CONNECTIONS AND @@global.slow_query_log=$slow AND @@global.long_query_time=$OPS_PARAM_LONG_QUERY_TIME AND @@global.default_storage_engine='$OPS_PARAM_STORAGE_ENGINE');
SQL
)
[[ "$result" == 1 ]] || { echo '配置修改结果尚未确认'; exit 1; }
