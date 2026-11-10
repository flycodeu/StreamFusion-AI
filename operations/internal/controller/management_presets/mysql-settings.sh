query <<'SQL'
SELECT JSON_OBJECT('columns',JSON_ARRAY('配置项','当前值'),'total',5,'rows',JSON_ARRAY(JSON_OBJECT('配置项','max_connections','当前值',CAST(@@global.max_connections AS CHAR)),JSON_OBJECT('配置项','slow_query_log','当前值',CAST(@@global.slow_query_log AS CHAR)),JSON_OBJECT('配置项','long_query_time','当前值',CAST(@@global.long_query_time AS CHAR)),JSON_OBJECT('配置项','bind_address','当前值',CAST(@@global.bind_address AS CHAR)),JSON_OBJECT('配置项','default_storage_engine','当前值',CAST(@@global.default_storage_engine AS CHAR))));
SQL
