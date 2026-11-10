check_owner
for path in "$root/data" "$root/config" "$root/logs"; do
  [[ ! -L "$path" ]] || fail '本次数据目录已被替换为符号链接'
  check_no_mounts "$path"
done
rm -rf -- "$root/data" "$root/config" "$root/logs"
printf '%s\n' '本次标注数据和配置已删除，外部数据集保留'
