[[ -f "$site" || -f "$disabled" ]] || { echo '站点不存在'; exit 1; }
rm -f -- "$site" "$disabled"
render
