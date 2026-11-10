check_owner
state=$(systemctl show --property=ActiveState --value --no-pager "$OPS_SOFTWARE_LOCATOR")
case "$state" in active) state=运行中;; inactive) state=已停止;; failed) state=启动失败;; activating) state=启动中;; *) state=状态待确认;; esac
printf '服务状态：%s\n当前监听与上传配置：\n' "$state"
cat -- "$root/config/ytlabel.json"
printf '\n'
