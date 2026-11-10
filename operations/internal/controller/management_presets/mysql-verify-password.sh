account_name
proof="$OPS_INSTALL_ROOT/work/.mysql-proof-$OPS_JOB_ID"
digest=$( { printf '%s\n' "$OPS_JOB_ID" "$OPS_OPERATION_KEY" "$OPS_PARAM_USERNAME" "$OPS_PARAM_HOST"; sha256sum "$OPS_SECRET_PASSWORD_FILE" | cut -d' ' -f1; } | sha256sum | cut -d' ' -f1 )
[[ -f "$proof" && ! -L "$proof" && "$(head -n 1 "$proof")" == "$digest" ]] || { echo '缺少本次密码修改回执'; exit 1; }
auth=$(query <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
SELECT SHA2(CONCAT(plugin,':',authentication_string),256) FROM mysql.user WHERE User=$user_sql AND Host=$host_sql;
SQL
)
[[ "$auth" =~ ^[a-f0-9]{64}$ && "$auth" == "$(tail -n 1 "$proof")" ]] || { echo '账号密码修改结果尚未确认'; exit 1; }
