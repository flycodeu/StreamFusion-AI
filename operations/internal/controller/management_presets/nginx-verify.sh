[[ -f "$proof" && ! -L "$proof" && "$(head -n 1 "$proof")" == "$(input_hash)" ]] || { echo '缺少本次站点修改回执'; exit 1; }
actual=$( { input_hash; file_hash "$site"; file_hash "$disabled"; file_hash "$main"; } )
[[ "$actual" == "$(cat "$proof")" ]] || { echo '站点配置在修改后发生变化'; exit 1; }
nginx_test
