database_name
account_name
case "$OPS_PARAM_PERMISSION" in 只读) required=2; privileges="'SELECT','SHOW VIEW'";; 读写) required=15; privileges="'SELECT','INSERT','UPDATE','DELETE','CREATE','ALTER','INDEX','DROP','SHOW VIEW','CREATE VIEW','TRIGGER','EXECUTE','CREATE ROUTINE','ALTER ROUTINE','EVENT'";; *) exit 1;; esac
# Verify each granted privilege in this database; no global privileges are added.
result=$(query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
SELECT COUNT(DISTINCT PRIVILEGE_TYPE) FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE=CONCAT(CHAR(39),$user_sql,CHAR(39),'@',CHAR(39),$host_sql,CHAR(39)) AND REPLACE(TABLE_SCHEMA,CHAR(92),'')=$(literal "$OPS_PARAM_DATABASE") AND PRIVILEGE_TYPE IN ($privileges);
SQL
)
[[ "$result" == "$required" ]] || { echo '数据库授权结果尚未确认'; exit 1; }
