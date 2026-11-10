database_name
[[ "$OPS_PARAM_CHARSET" == utf8mb4 && "$OPS_PARAM_COLLATION" =~ ^utf8mb4_(0900_ai_ci|general_ci|bin)$ ]] || exit 1
query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
CREATE DATABASE \`$OPS_PARAM_DATABASE\` CHARACTER SET utf8mb4 COLLATE $OPS_PARAM_COLLATION;
SQL
printf '数据库已创建\n'
