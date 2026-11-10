check_no_mounts() {
  local path="$1" target
  need findmnt
  findmnt --raw --noheadings --output TARGET > "$root/work/mount-targets" || fail '无法确认数据目录内的挂载点'
  while IFS= read -r target; do
    [[ "$target" != "$path" && "$target" != "$path/"* ]] || fail "目录包含挂载点，请管理员先核对：$target"
  done < "$root/work/mount-targets"
}
