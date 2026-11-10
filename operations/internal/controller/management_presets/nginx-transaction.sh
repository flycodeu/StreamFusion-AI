mkdir -p "$sites"
stage=$(mktemp -d "$OPS_INSTALL_ROOT/work/.nginx-edit.XXXXXX")
for entry in main site disabled; do
  file=${!entry}
  if [[ -f "$file" ]]; then cp -- "$file" "$stage/$entry"; fi
done
restore() {
  trap - ERR INT TERM
  set +e
  local failed=0
  for entry in main site disabled; do
    file=${!entry}
    if [[ -f "$stage/$entry" ]]; then cat "$stage/$entry" > "$file" || failed=1; else rm -f -- "$file" || failed=1; fi
  done
  nginx_test && nginx_reload || failed=1
  if [[ "$failed" == 0 ]]; then echo '已恢复原配置'; else echo '原配置恢复需要检查'; fi
  rm -rf -- "$stage"
  exit 1
}
trap restore ERR INT TERM
render() {
  if [[ "$official" == true ]]; then
    if [[ -f "$site" ]]; then cat "$site" > "$main"; else rm -f -- "$main"; fi
  else
    base="$sites/base.conf.saved"
    if [[ ! -f "$base" ]]; then
      grep -Fq "$OPS_SOFTWARE_ID" "$main"
      cp -- "$main" "$base"
    fi
    [[ ! -L "$base" ]]
    sed '$s/}[[:space:]]*$//' "$base" > "$stage/candidate"
    for fragment in "$sites"/*.conf; do [[ ! -f "$fragment" ]] || cat "$fragment" >> "$stage/candidate"; done
    printf '\n}\n' >> "$stage/candidate"
    # Preserve the inode used by Docker's single-file bind mount.
    cat "$stage/candidate" > "$main"
  fi
  nginx_test
  nginx_reload
  { input_hash; file_hash "$site"; file_hash "$disabled"; file_hash "$main"; } > "$proof"
  trap - ERR INT TERM
  rm -rf -- "$stage"
  echo '站点配置已生效'
}
