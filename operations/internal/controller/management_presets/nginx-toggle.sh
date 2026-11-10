case "$OPS_PARAM_ENABLED" in
  true) [[ -f "$disabled" ]]; mv -- "$disabled" "$site";;
  false) [[ -f "$site" ]]; mv -- "$site" "$disabled";;
  *) exit 1;;
esac
render
