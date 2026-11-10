account_name
proof="$OPS_INSTALL_ROOT/work/.mysql-proof-$OPS_JOB_ID"
[[ -f "$OPS_SECRET_PASSWORD_FILE" ]] || exit 1
[[ "$(wc -l < "$OPS_SECRET_PASSWORD_FILE")" == 0 ]] || exit 1
password=$(cat "$OPS_SECRET_PASSWORD_FILE")
[[ ${#password} -ge 8 && ${#password} -le 256 && "$password" != *$'\n'* && "$password" != *$'\r'* ]] || { echo '密码须8至256字节且不含换行'; exit 1; }
case "$OPS_OPERATION_KEY" in create-user) verb='CREATE USER';; change-password) verb='ALTER USER';; *) exit 1;; esac
query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
$verb $user_sql@$host_sql IDENTIFIED BY $(literal "$password");
SQL
unset password
digest=$( { printf '%s\n' "$OPS_JOB_ID" "$OPS_OPERATION_KEY" "$OPS_PARAM_USERNAME" "$OPS_PARAM_HOST"; sha256sum "$OPS_SECRET_PASSWORD_FILE" | cut -d' ' -f1; } | sha256sum | cut -d' ' -f1 )
auth=$(query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
SELECT SHA2(CONCAT(plugin,':',authentication_string),256) FROM mysql.user WHERE User=$user_sql AND Host=$host_sql;
SQL
)
[[ "$auth" =~ ^[a-f0-9]{64}$ ]] || exit 1
printf '%s\n%s\n' "$digest" "$auth" > "$proof"
printf '账号密码已保存\n'
