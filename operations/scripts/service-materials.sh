#!/usr/bin/env bash
# Distribute public release packages beside the installed controller. This never
# opens state, executes material packages, deletes old files, or contacts targets.

service_materials_files=()
service_materials_hashes=()
service_materials_sizes=()
service_materials_source_dir=

service_materials_destination() {
  local prefix=$1 directory="$1/materials" file index
  [[ ! -e $directory && ! -L $directory ]] && return 0
  target_materials_protected_directory "$directory" && [[ $(stat -c %a "$directory") == 755 ]] || fail '安装材料目录须为root持有的0755普通目录'
  for index in "${!service_materials_files[@]}"; do
    file=${service_materials_files[$index]}
    if [[ -e $directory/$file || -L $directory/$file ]]; then
      target_materials_protected_file "$directory/$file" && [[ $(stat -c %a "$directory/$file") == 644 ]] || fail '已有安装材料文件权限或路径不安全'
      [[ $(timeout 300 sha256sum -- "$directory/$file" | cut -d' ' -f1) == "${service_materials_hashes[$index]}" ]] || fail '已有安装材料内容不同，拒绝覆盖；保留现有材料和状态'
    fi
  done
}

service_materials_preflight() {
  local prefix=$1 binary=$2 directory listing file checksum sums size total=0
  service_materials_files=()
  service_materials_hashes=()
  service_materials_sizes=()
  service_materials_source_dir=
  directory="$(dirname "$binary")/materials"
  [[ -e $directory || -L $directory ]] || return 0
  [[ -d $directory && ! -L $directory ]] || fail '发行材料目录不允许为链接或特殊文件'
  [[ -e $directory/library.json || -L $directory/library.json ]] || return 0
  # The compiled check is read-only and has its own 15-minute transfer bound.
  listing=$(timeout 910 "$binary" material-library-check --material-library "$directory") || fail '发行材料库检查失败，尚未修改账号或状态'
  [[ -n $listing ]] || fail '发行材料库没有返回清单'
  sums="$(dirname "$binary")/SHA256SUMS"
  [[ -f $sums && ! -L $sums && $(stat -c %s "$sums") -le 1048576 ]] || fail '携带材料时必须提供普通且有界的发行SHA256SUMS'
  while IFS= read -r file; do
    # Defense in depth before using the checker output as a path or utility arg.
    [[ -n $file && $file != . && $file != .. && $file != */* && $file != *\\* && $file != *$'\r'* && $file != *$'\n'* ]] || fail '材料库检查输出路径不正确'
    [[ -f $directory/$file && ! -L $directory/$file ]] || fail '发行材料文件已变化'
    size=$(stat -c %s "$directory/$file")
    [[ $size =~ ^[0-9]+$ ]] && ((size > 0 && size <= 2147483648)) || fail '发行材料文件大小超过限制'
    if [[ $file == library.json ]]; then
      ((size <= 1048576)) || fail '发行材料清单过大'
    else
      total=$((total + size))
      ((total <= 17179869184)) || fail '发行材料总大小超过限制'
    fi
    checksum=$(timeout 300 sha256sum -- "$directory/$file" | cut -d' ' -f1)
    grep -Fxq -- "$checksum  materials/$file" "$sums" || fail '发行材料未被SHA256SUMS覆盖或内容已变化'
    service_materials_files+=("$file")
    service_materials_hashes+=("$checksum")
    service_materials_sizes+=("$size")
  done <<< "$listing"
  [[ ${service_materials_files[0]} == library.json ]] || fail '材料库检查未返回清单文件'
  service_materials_source_dir=$directory
  service_materials_destination "$prefix"
}

service_materials_install() (
  local prefix=$1 binary=$2 directory="$1/materials" stage= file index staged_listing expected_listing
  [[ -n $service_materials_source_dir ]] || exit 0
  # Recheck destination before publishing; never overwrite existing bytes.
  service_materials_destination "$prefix"
  stage=$(mktemp -d "$prefix/.materials-XXXXXX")
  cleanup_service_materials() {
    [[ -z $stage ]] && return
    for file in "${service_materials_files[@]}"; do rm -f -- "$stage/$file"; done
    rmdir -- "$stage" 2>/dev/null || true
  }
  trap cleanup_service_materials EXIT
  for index in "${!service_materials_files[@]}"; do
    file=${service_materials_files[$index]}
    # Limit the copy even if a source grows after preflight.
    [[ -f $service_materials_source_dir/$file && ! -L $service_materials_source_dir/$file ]] || fail '发行材料文件已变化'
    timeout 300 head -c "$((service_materials_sizes[$index] + 1))" -- "$service_materials_source_dir/$file" > "$stage/$file"
    [[ $(stat -c %s "$stage/$file") == "${service_materials_sizes[$index]}" &&
       $(timeout 300 sha256sum -- "$stage/$file" | cut -d' ' -f1) == "${service_materials_hashes[$index]}" ]] || fail '材料复制期间内容变化，拒绝发布'
    chown root:root -- "$stage/$file"
    chmod 0644 -- "$stage/$file"
  done
  staged_listing=$(timeout 910 "$binary" material-library-check --material-library "$stage") || fail '暂存材料库检查失败'
  expected_listing=$(printf '%s\n' "${service_materials_files[@]}")
  [[ $staged_listing == "$expected_listing" ]] || fail '材料清单在预检之后发生变化，拒绝发布'
  chmod 0755 "$stage"
  if [[ ! -e $directory && ! -L $directory ]]; then
    mv -T -n -- "$stage" "$directory"
    [[ ! -d $stage ]] || fail '材料目录在安装期间出现，拒绝覆盖'
    stage=
    exit 0
  fi
  service_materials_destination "$prefix"
  # Publish library.json last. Hard-link creation fails if a path races us; it
  # cannot replace a file or follow a destination link. Existing extras survive.
  for ((index=${#service_materials_files[@]}-1; index>=0; index--)); do
    file=${service_materials_files[$index]}
    if [[ ! -e $directory/$file && ! -L $directory/$file ]]; then
      ln -- "$stage/$file" "$directory/$file" || fail '材料目标文件在安装期间出现，拒绝覆盖'
    fi
  done
  service_materials_destination "$prefix"
)
