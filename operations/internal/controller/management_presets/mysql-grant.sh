database_name
account_name
case "$OPS_PARAM_PERMISSION" in 只读) grants='SELECT, SHOW VIEW';; 读写) grants='SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, DROP, SHOW VIEW, CREATE VIEW, TRIGGER, EXECUTE, CREATE ROUTINE, ALTER ROUTINE, EVENT';; *) exit 1;; esac
grant_database=$OPS_PARAM_DATABASE
partial_revokes=$(query <<'SQL'
SELECT @@global.partial_revokes;
SQL
)
[[ "$partial_revokes" == 0 || "$partial_revokes" == 1 ]] || exit 1
if [[ "$partial_revokes" == 0 ]]; then grant_database=${grant_database//_/\\_}; fi
query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
GRANT $grants ON \`$grant_database\`.* TO $user_sql@$host_sql;
SQL
printf '数据库权限已授予\n'
