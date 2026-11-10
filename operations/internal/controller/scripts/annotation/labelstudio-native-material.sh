check_owner
[[ -x "$OPS_PARAM_PYTHON_BINARY" && ! -L "$root/runtime" ]] || fail 'Python 可执行文件或安装目录不可用'
"$OPS_PARAM_PYTHON_BINARY" -c 'import sys,sqlite3,venv; assert sys.version_info[:2]==(3,12), "需要Python 3.12"; assert sqlite3.sqlite_version_info >= (3,35), "需要SQLite 3.35以上"'
"$OPS_PARAM_PYTHON_BINARY" -m venv "$root/runtime/venv"
python="$root/runtime/venv/bin/python"
wheel='label_studio-@@VERSION@@-py3-none-any.whl'
if [[ "$OPS_SOURCE" == ONLINE ]]; then
  curl --fail --location --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 600 --retry 2 --output "$root/work/$wheel" '@@WHEEL_URL@@'
  printf '%s  %s\n' '@@WHEEL_SHA@@' "$root/work/$wheel" | sha256sum -c -
  "$python" -m pip --isolated install --disable-pip-version-check --no-cache-dir --timeout 30 --retries 2 --index-url https://pypi.org/simple "$root/work/$wheel"
else
  bundle="$root/work/wheelhouse"
  mkdir -m 700 "$bundle"
  tar -tzf "$OPS_ARTIFACT_PATH" > "$root/work/wheel-files"
  [[ $(sort -u "$root/work/wheel-files" | wc -l) -eq $(wc -l < "$root/work/wheel-files") ]] || fail '离线包存在重复条目'
  while IFS= read -r name; do [[ "$name" =~ ^(SHA256SUMS|requirements.txt|wheels/|wheels/[A-Za-z0-9_.+-]+\.whl)$ ]] || fail '离线包须仅包含 SHA256SUMS、requirements.txt、wheels/*.whl'; done < "$root/work/wheel-files"
  tar -tvzf "$OPS_ARTIFACT_PATH" | awk 'substr($1,1,1)!="-" && substr($1,1,1)!="d" {bad=1} {bytes+=$3; files++} END {if(bytes>2147483648 || files>4096)bad=1; exit bad}' || fail '离线包存在链接、特殊文件，或解压内容超过 2 GiB / 4096 个文件'
  tar -xzf "$OPS_ARTIFACT_PATH" --no-same-owner --no-same-permissions -C "$bundle"
  [[ -f "$bundle/SHA256SUMS" && -f "$bundle/requirements.txt" ]] || fail '离线包缺少摘要清单或依赖锁定文件'
  [[ $(wc -c < "$bundle/SHA256SUMS") -le 524288 && $(wc -c < "$bundle/requirements.txt") -le 262144 ]] || fail '摘要或依赖清单过大'
  while IFS= read -r line || [[ -n "$line" ]]; do [[ "$line" =~ ^[a-fA-F0-9]{64}\ \ (requirements.txt|wheels/[A-Za-z0-9_.+-]+\.whl)$ ]] || fail '摘要清单格式不正确'; done < "$bundle/SHA256SUMS"
  (cd "$bundle"; sha256sum --strict -c SHA256SUMS)
  cut -c 67- "$bundle/SHA256SUMS" | sort > "$root/work/manifest-paths"
  { printf 'requirements.txt\n'; find "$bundle/wheels" -maxdepth 1 -type f -name '*.whl' -printf 'wheels/%f\n'; } | sort > "$root/work/wheel-paths"
  cmp -s "$root/work/manifest-paths" "$root/work/wheel-paths" || fail '摘要清单必须完整且无重复地覆盖全部文件'
  while IFS= read -r line || [[ -n "$line" ]]; do [[ "$line" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]*==[A-Za-z0-9_.+!-]+$ ]] || fail 'requirements.txt 每行须为 包名==版本'; done < "$bundle/requirements.txt"
  grep -Eiq '^label[-_]studio==@@VERSION@@$' "$bundle/requirements.txt" || fail '离线包不属于 Label Studio @@VERSION@@'
  [[ -f "$bundle/wheels/$wheel" ]] || fail '离线包缺少官方 Label Studio wheel'
  printf '%s  %s\n' '@@WHEEL_SHA@@' "$bundle/wheels/$wheel" | sha256sum -c -
  "$python" -m pip --isolated install --disable-pip-version-check --no-cache-dir --no-index --find-links "$bundle/wheels" --no-deps -r "$bundle/requirements.txt"
fi
"$python" -m pip check
"$python" -c 'import importlib.metadata; assert importlib.metadata.version("label-studio")=="@@VERSION@@"'
"$python" -m pip freeze > "$root/config/installed-requirements.txt"
chmod -R a+rX "$root/runtime"
