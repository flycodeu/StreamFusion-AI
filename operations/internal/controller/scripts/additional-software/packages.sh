install_packages() {
  local args=()
  if [[ "$OPS_SOURCE" == ONLINE ]]; then
    args=("$(cat "$root/config/package-request")")
  else
    shopt -s nullglob
    args=("$root/work/packages/"*.deb)
    [[ ${#args[@]} -gt 0 ]] || fail '离线依赖包不存在'
  fi
  local network=()
  [[ "$OPS_SOURCE" == ONLINE ]] || network=(--no-download)
  local source_options=()
  if [[ "$OPS_SOURCE" == ONLINE ]]; then
    # Include the official list alongside the existing system sources without leaving a global repository file.
    install -d -m 755 "$root/work/apt-sources"
    find /etc/apt/sources.list.d -maxdepth 1 -type f \( -name '*.list' -o -name '*.sources' \) -exec ln -s '{}' "$root/work/apt-sources/" \;
    ln -s "$root/config/official.list" "$root/work/apt-sources/ops-official.list"
    source_options=(-o "Dir::Etc::sourceparts=$root/work/apt-sources")
  fi
  apt-get "${source_options[@]}" -o DPkg::Lock::Timeout=60 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 -o Acquire::Retries=2 --simulate --no-remove "${network[@]}" install "${args[@]}" > "$root/work/package-apply-plan" 2>&1 || { cat "$root/work/package-apply-plan"; fail '依赖软件包不完整或版本冲突，请补齐依赖后重试'; }
  grep -Eq '^Inst [^ ]+ \[' "$root/work/package-apply-plan" && fail '安装将升级已有系统包，请先由管理员核对版本'
  cat "$root/work/package-apply-plan"
  apt-get "${source_options[@]}" -o DPkg::Lock::Timeout=60 -o Dpkg::Options::=--force-confold -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 -o Acquire::Retries=2 --yes --no-remove --no-install-recommends "${network[@]}" install "${args[@]}"
  local package=postgresql-18
  [[ "$app" != gitlab ]] || package=gitlab-ce
  dpkg-query -W -f='${Version}' "$package" > "$root/config/owned-package-version"
}
check_owned_package() {
  local package="$1"
  [[ -f "$root/config/owned-package-version" && "$(dpkg-query -W -f='${Version}' "$package" 2>/dev/null)" == "$(cat "$root/config/owned-package-version")" ]] || fail '系统软件包版本已被外部修改，请先核对归属'
  apt-get -o DPkg::Lock::Timeout=60 --simulate remove "$package" > "$root/work/package-remove-plan"
  while read -r action name _; do [[ "$action" != Remv || "$name" == "$package" ]] || fail "卸载会影响其他系统软件包：$name"; done < "$root/work/package-remove-plan"
}
remove_owned_package() {
  local package="$1"
  check_owned_package "$package"
  apt-get -o DPkg::Lock::Timeout=60 --yes --no-auto-remove remove "$package"
}
