check_owner
if [[ "$app" == postgresql || "$app" == gitlab ]]; then
  need apt-get
  if [[ "$OPS_SOURCE" == ONLINE ]]; then
    need gpg
    if [[ "$app" == postgresql ]]; then
      download https://www.postgresql.org/media/keys/ACCC4CF8.asc "$root/config/repository.asc"
      fingerprint="$(gpg --batch --show-keys --with-colons "$root/config/repository.asc" | awk -F: '$1=="fpr" {print $10; exit}')"
      [[ "$fingerprint" == B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8 ]] || fail 'PostgreSQL 仓库签名密钥不符'
      printf 'deb [signed-by=%s/config/repository.asc] https://apt.postgresql.org/pub/repos/apt noble-pgdg main\n' "$root" > "$root/config/official.list"
      package=postgresql-18; wanted='18.6-'
    else
      download https://packages.gitlab.com/gitlab/gitlab-ce/gpgkey "$root/config/repository.asc"
      gpg --batch --show-keys "$root/config/repository.asc" >/dev/null || fail 'GitLab 仓库签名密钥不可读取'
      printf 'deb [signed-by=%s/config/repository.asc] https://packages.gitlab.com/gitlab/gitlab-ce/ubuntu/ noble main\n' "$root" > "$root/config/official.list"
      package=gitlab-ce; wanted='19.5.3-ce.0'
    fi
    # The source file is local to this installation. Existing system sources remain in use for dependencies.
    chmod 755 "$root" "$root/config"
    chmod 644 "$root/config/repository.asc" "$root/config/official.list"
    apt-get -o DPkg::Lock::Timeout=60 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 -o Acquire::Retries=2 update
    apt-get -o Dir::Etc::sourcelist="$root/config/official.list" -o Dir::Etc::sourceparts=- -o Acquire::https::Timeout=30 -o Acquire::Retries=2 update
    package_version="$(apt-cache -o Dir::Etc::sourcelist="$root/config/official.list" -o Dir::Etc::sourceparts=- madison "$package" | awk -F'|' -v v="$wanted" '{gsub(/ /,"",$2); if(index($2,v)==1){print $2; exit}}')"
    [[ -n "$package_version" ]] || fail '官方仓库未找到此版本，请选择可用版本或上传完整离线包'
    printf '%s=%s\n' "$package" "$package_version" > "$root/config/package-request"
  else
    # Offline bundle format: SHA256SUMS and packages/*.deb, prepared for Ubuntu 24.04 amd64.
    archive="$OPS_ARTIFACT_PATH"
    [[ -f "$archive" && ! -L "$archive" ]] || fail '请上传包含 SHA256SUMS 与 packages/*.deb 的完整离线包'
    install -d -m 700 "$root/work/packages"
    tar -tzf "$archive" > "$root/work/archive-list"
    [[ $(sort -u "$root/work/archive-list" | wc -l) -eq $(wc -l < "$root/work/archive-list") ]] || fail '离线包包含重复文件'
    while IFS= read -r name; do [[ "$name" =~ ^(SHA256SUMS|packages/|packages/[A-Za-z0-9_.+~%-]+\.deb)$ ]] || fail '离线包只能包含 SHA256SUMS 和 packages/*.deb'; done < "$root/work/archive-list"
    tar -tvzf "$archive" | awk 'substr($1,1,1)!="-" && substr($1,1,1)!="d" {bad=1} END {exit bad}' || fail '离线包包含链接或特殊文件'
    tar -xzf "$archive" --no-same-owner --no-same-permissions -C "$root/work"
    [[ -f "$root/work/SHA256SUMS" ]] || fail '离线包缺少 SHA256SUMS'
    while IFS= read -r line; do [[ "$line" =~ ^[a-fA-F0-9]{64}\ [\ \*]packages/[A-Za-z0-9_.+~%-]+\.deb$ ]] || fail '摘要清单格式不正确'; done < "$root/work/SHA256SUMS"
    (cd "$root/work"; sha256sum --strict -c SHA256SUMS)
    shopt -s nullglob
    packages=("$root/work/packages/"*.deb)
    [[ ${#packages[@]} -gt 0 && ${#packages[@]} -eq $(wc -l < "$root/work/SHA256SUMS") ]] || fail '离线软件包与摘要清单不一致'
    cut -c 67- "$root/work/SHA256SUMS" | sort > "$root/work/manifest-paths"
    [[ $(sort -u "$root/work/manifest-paths" | wc -l) -eq ${#packages[@]} ]] || fail '摘要清单包含重复文件'
    for file in "${packages[@]}"; do printf 'packages/%s\n' "${file##*/}"; done | sort > "$root/work/package-paths"
    cmp -s "$root/work/manifest-paths" "$root/work/package-paths" || fail '摘要清单未完整覆盖离线软件包'
    found=false
    for file in "${packages[@]}"; do
      arch="$(dpkg-deb -f "$file" Architecture)"; [[ "$arch" == amd64 || "$arch" == all ]] || fail '离线包架构不支持'
      name="$(dpkg-deb -f "$file" Package)"; package_version="$(dpkg-deb -f "$file" Version)"
      if [[ "$app" == postgresql && "$name" == postgresql-18 ]]; then [[ "$package_version" == 18.6-* ]] || fail '需要 PostgreSQL 18.6 的软件包'; found=true; fi
      if [[ "$app" == gitlab && "$name" == gitlab-ce ]]; then [[ "$package_version" == 19.5.3-ce.0 ]] || fail '需要 GitLab 19.5.3-ce.0 的软件包'; found=true; fi
    done
    [[ "$found" == true ]] || fail '离线包缺少所选软件的正确版本'
    apt-get --simulate --no-download --no-remove install "${packages[@]}" > "$root/work/package-plan" 2>&1 || { cat "$root/work/package-plan"; fail '离线依赖不完整，请补齐 Ubuntu 24.04 amd64 的依赖软件包'; }
    grep -Eq '^Inst [^ ]+ \[' "$root/work/package-plan" && fail '离线包将升级已有系统软件包，请先由管理员核对依赖版本'
    cat "$root/work/package-plan"
  fi
else
  archive="$root/work/release"
  if [[ "$OPS_SOURCE" == ONLINE ]]; then download '@@URL@@' "$archive"; else cp -- "$OPS_ARTIFACT_PATH" "$archive"; fi
  printf '%s  %s\n' '@@SHA256@@' "$archive" | sha256sum -c -
  if [[ "$app" == jenkins ]]; then
    install -d -m 755 "$root/runtime"
    install -m 644 "$archive" "$root/runtime/jenkins.war"
  else
    tar -tzf "$archive" > "$root/work/archive-list"
    grep -Eq '(^/|(^|/)\.\.(/|$))' "$root/work/archive-list" && fail '安装包目录不安全'
    install -d -m 755 "$root/runtime"
    tar -xzf "$archive" --no-same-owner --no-same-permissions --strip-components=1 -C "$root/runtime"
    chmod -R a+rX "$root/runtime"
  fi
fi
