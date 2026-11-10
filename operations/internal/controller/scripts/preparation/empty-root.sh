# Only an unused directory owned by root or this SSH account may be recreated.
prepare_empty_root() {
  local root=$1 account=$2 parent owner account_uid permissions
  [[ -e "$root" || -L "$root" ]] || return 0
  [[ -d "$root" && ! -L "$root" ]] || fail '安装路径不是实际目录，已保留原文件。'
  [[ -z "$(find "$root" -mindepth 1 -maxdepth 1 -print -quit)" ]] || return 0
  [[ $(realpath -m -- "$root") == "$root" ]] || fail '安装目录包含符号链接或非规范路径，已停止。'
  owner=$(stat -c %u -- "$root")
  account_uid=$(id -u "$account")
  [[ $owner == 0 || $owner == "$account_uid" ]] || fail '空目录属于其他账号，已保留原目录。'
  parent=$(dirname -- "$root")
  while :; do
    [[ -d "$parent" && ! -L "$parent" && $(stat -c %u -- "$parent") == 0 ]] || fail '安装目录的上级目录归属不安全，已停止。'
    permissions=$(stat -c %a -- "$parent")
    [[ $((8#$permissions & 8#22)) -eq 0 ]] || fail '安装目录的上级目录允许其他账号写入，已停止。'
    [[ $parent == / ]] && break
    parent=$(dirname -- "$parent")
  done
  # rmdir is atomic: a mount point or a concurrent file prevents removal.
  rmdir -- "$root" || fail '空目录已变化或是挂载目录，已保留并停止安装。'
}
