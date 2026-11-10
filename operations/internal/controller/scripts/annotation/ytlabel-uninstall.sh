check_owner
check_no_mounts "$root/runtime"
path="/etc/systemd/system/$unit"
[[ -f "$path" && ! -L "$path" ]] || fail '缺少原服务单元，请先核对卸载范围'
grep -Fxq "Description=Ops ytlabel $OPS_SOFTWARE_ID" "$path" || fail '服务单元归属不符'
systemctl disable --now "$unit"
rm -- "$path"
systemctl daemon-reload
rm -rf -- "$root/runtime"
rm -f -- "$root/bin/run" "$root/bin/ytlabel-launch.py"
printf '%s\n' 'ytlabel 已卸载，标注数据和配置已保留；外部素材目录未删除'
