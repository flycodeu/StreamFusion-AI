check_owner
for path in "$root/data" "$root/config" "$root/logs"; do check_no_mounts "$path"; done
rm -rf -- "$root/data" "$root/config" "$root/logs"
printf '%s\n' '本实例标注数据和配置已删除；外部素材目录未删除'
