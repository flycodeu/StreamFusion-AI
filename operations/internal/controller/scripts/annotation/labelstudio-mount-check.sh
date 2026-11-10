check_no_mounts() {
  local directory="$1" mounted
  need findmnt
  findmnt --raw --noheadings --output TARGET > "$root/work/mount-targets" || fail '无法核对挂载目录，请管理员检查'
  while IFS= read -r mounted; do
    [[ "$mounted" != "$directory" && "$mounted" != "$directory/"* ]] || fail "目录包含挂载点，请先核对：$mounted"
  done < "$root/work/mount-targets"
}
