database_name
result=$(query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME=$(literal "$OPS_PARAM_DATABASE") AND DEFAULT_CHARACTER_SET_NAME=$(literal "$OPS_PARAM_CHARSET") AND DEFAULT_COLLATION_NAME=$(literal "$OPS_PARAM_COLLATION");
SQL
)
[[ "$result" == 1 ]] || { echo '数据库创建结果尚未确认'; exit 1; }
