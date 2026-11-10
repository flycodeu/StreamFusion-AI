[[ "$OPS_PARAM_SITE" =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,47}$ ]] || { echo '站点名称格式无效'; exit 1; }
site="$sites/$OPS_PARAM_SITE.conf"
disabled="$site.disabled"
if [[ "$official" == true ]]; then main="/etc/nginx/conf.d/ops-$OPS_SOFTWARE_ID-$OPS_PARAM_SITE.conf"; fi
for file in "$sites" "$site" "$disabled" "$main"; do [[ ! -L "$file" ]] || { echo '配置路径不能是符号链接'; exit 1; }; done
proof="$OPS_INSTALL_ROOT/work/.nginx-proof-$OPS_JOB_ID"
file_hash() { if [[ -f "$1" ]]; then sha256sum "$1" | cut -d' ' -f1; else printf 'ABSENT\n'; fi; }
input_hash() { { printf '%s\n' "$OPS_JOB_ID" "$OPS_OPERATION_KEY"; env | LC_ALL=C sort | sed -n '/^OPS_PARAM_/p'; } | sha256sum | cut -d' ' -f1; }
