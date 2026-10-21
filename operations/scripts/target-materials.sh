#!/usr/bin/env bash
# Shared by the explicit controller installer/uninstaller. This only distributes
# read-only target preparation files; it never initializes or contacts a target.

target_materials_source() {
  local binary=$1 script_dir=$2 candidate
  candidate="$(dirname "$binary")/install-target.sh"
  if [[ -f $candidate && ! -L $candidate ]]; then
    printf '%s\n' "$candidate"
  else
    printf '%s/install-target.sh\n' "$script_dir"
  fi
}

target_materials_protected_directory() {
  local directory=$1 mode
  [[ -d $directory && ! -L $directory && $(stat -c %u "$directory") == 0 ]] || return 1
  mode=$(stat -c %a "$directory")
  (( (0$mode & 022) == 0 ))
}

target_materials_protected_file() {
  local file=$1 mode
  [[ -f $file && ! -L $file && $(stat -c %u "$file") == 0 ]] || return 1
  mode=$(stat -c %a "$file")
  (( (0$mode & 022) == 0 ))
}

target_materials_validate() {
  local prefix=$1 binary_sha=$2 script_sha=${3:-} directory="$1/release/0.2.0" marker expected file
  [[ ! -e $directory && ! -L $directory ]] && return 0
  target_materials_protected_directory "$prefix" &&
    target_materials_protected_directory "$prefix/release" &&
    target_materials_protected_directory "$directory" || fail '目标准备材料目录权限或路径不安全'
  marker="$directory/.ops-target-materials"
  target_materials_protected_file "$marker" || fail '目标准备目录不属于该安装，拒绝覆盖'
  if [[ -z $script_sha ]]; then script_sha=$(sed -n 's/^script-sha256=//p' "$marker"); fi
  [[ $script_sha =~ ^[a-f0-9]{64}$ ]] || fail '目标准备材料归属标记不正确'
  expected=$(printf 'schema=1\ncontroller-sha256=%s\nscript-sha256=%s' "$binary_sha" "$script_sha")
  [[ $(cat "$marker") == "$expected" ]] || fail '已有目标准备材料归属或版本不一致'
  for file in streamfusion-ops install-target.sh SHA256SUMS; do
    target_materials_protected_file "$directory/$file" || fail '目标准备材料文件缺失、权限或归属不正确'
  done
  [[ $(sha256sum "$directory/streamfusion-ops" | cut -d' ' -f1) == "$binary_sha" &&
     $(sha256sum "$directory/install-target.sh" | cut -d' ' -f1) == "$script_sha" &&
     $(cat "$directory/SHA256SUMS") == "$(printf '%s  streamfusion-ops\n%s  install-target.sh' "$binary_sha" "$script_sha")" ]] || fail '已有目标准备材料已变化，拒绝覆盖或删除'
}

target_materials_preflight() {
  local prefix=$1 binary_sha=$2 script=$3 script_sha manifest
  [[ -f $script && ! -L $script && $(stat -c %s "$script") -gt 0 && $(stat -c %s "$script") -le 65536 ]] || fail '发行包缺少普通的install-target.sh'
  script_sha=$(sha256sum "$script" | cut -d' ' -f1)
  manifest="$(dirname "$script")/SHA256SUMS"
  if [[ -e $manifest || -L $manifest ]]; then
    [[ -f $manifest && ! -L $manifest ]] &&
      grep -Fxq "$script_sha  install-target.sh" "$manifest" &&
      grep -Fxq "$binary_sha  streamfusion-ops" "$manifest" || fail '同发行包的目标准备脚本或二进制摘要不匹配'
  fi
  if [[ -e $prefix || -L $prefix ]]; then
    target_materials_protected_directory "$prefix" || fail '程序目录须由root持有且不可由普通账号写入'
  fi
  if [[ -e $prefix/release || -L $prefix/release ]]; then
    target_materials_protected_directory "$prefix/release" || fail '发行材料父目录权限或路径不安全'
  fi
  target_materials_validate "$prefix" "$binary_sha" "$script_sha"
}

target_materials_install() (
  local prefix=$1 binary=$2 binary_sha=$3 script=$4 directory="$1/release/0.2.0" stage= script_sha
  target_materials_preflight "$prefix" "$binary_sha" "$script"
  [[ ! -e $directory ]] || exit 0
  script_sha=$(sha256sum "$script" | cut -d' ' -f1)
  install -d -m 0755 -o root -g root "$prefix/release"
  stage=$(mktemp -d "$prefix/release/.target-0.2.0-XXXXXX")
  cleanup_target_materials() {
    [[ -z $stage ]] && return
    # Only the four files created here are removed; never recurse into a path.
    rm -f -- "$stage/streamfusion-ops" "$stage/install-target.sh" "$stage/SHA256SUMS" "$stage/.ops-target-materials"
    rmdir -- "$stage" 2>/dev/null || true
  }
  trap cleanup_target_materials EXIT
  install -m 0555 -o root -g root "$binary" "$stage/streamfusion-ops"
  install -m 0444 -o root -g root "$script" "$stage/install-target.sh"
  [[ $(sha256sum "$stage/streamfusion-ops" | cut -d' ' -f1) == "$binary_sha" &&
     $(sha256sum "$stage/install-target.sh" | cut -d' ' -f1) == "$script_sha" ]] || fail '目标准备材料暂存摘要不一致'
  printf '%s  streamfusion-ops\n%s  install-target.sh\n' "$binary_sha" "$script_sha" > "$stage/SHA256SUMS"
  printf 'schema=1\ncontroller-sha256=%s\nscript-sha256=%s\n' "$binary_sha" "$script_sha" > "$stage/.ops-target-materials"
  chmod 0444 "$stage/SHA256SUMS" "$stage/.ops-target-materials"
  chmod 0755 "$stage"
  mv -T -n "$stage" "$directory"
  [[ ! -d $stage ]] || fail '目标准备目录在安装期间出现，拒绝覆盖'
  stage=
)

target_materials_remove() {
  local prefix=$1 binary_sha=$2 directory="$1/release/0.2.0"
  [[ -e $directory || -L $directory ]] || return 0
  target_materials_validate "$prefix" "$binary_sha"
  rm -f -- "$directory/streamfusion-ops" "$directory/install-target.sh" "$directory/SHA256SUMS" "$directory/.ops-target-materials"
  rmdir -- "$directory" 2>/dev/null || true
  rmdir -- "$prefix/release" 2>/dev/null || true
}
