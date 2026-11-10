check_owner
python=$(cat "$root/config/python-binary")
group=$(id -gn "$service_user")
[[ ! -e "$root/runtime" ]] || fail '程序目录已存在，请先核对原任务'
install -d -m 750 -o root -g "$group" "$root/runtime"
bundle="$root/work/ytlabel-material"
[[ ! -e "$bundle" ]] || fail '材料暂存目录已存在，请先核对原任务'
install -d -m 700 "$bundle"
cat > "$root/work/ytlabel-extract.py" <<'PY'
@@EXTRACT@@
PY
if [[ "$OPS_SOURCE" == ONLINE ]]; then
  curl --fail --location --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 180 --max-filesize 5242880 --retry 2 --output "$bundle/source.tar.gz" '@@SOURCE_URL@@'
else
  "$python" -I "$root/work/ytlabel-extract.py" "$OPS_ARTIFACT_PATH" "$bundle" offline
fi
printf '%s  %s\n' '@@SOURCE_SHA256@@' "$bundle/source.tar.gz" | sha256sum -c -
"$python" -I "$root/work/ytlabel-extract.py" "$bundle/source.tar.gz" "$root/runtime/source" source
readable_material=("$root/runtime/source")
if [[ "$OPS_SOURCE" == ARCHIVE ]]; then
  mv -- "$bundle/wheelhouse" "$root/runtime/wheelhouse"
  readable_material+=("$root/runtime/wheelhouse")
fi
chown -hR root:"$group" "${readable_material[@]}"
find "${readable_material[@]}" -type d -exec chmod 750 {} +
find "${readable_material[@]}" -type f -exec chmod 640 {} +
install -d -m 750 -o "$service_user" -g "$group" "$root/runtime/venv"
runuser -u "$service_user" -- "$python" -I -m venv "$root/runtime/venv"
pip=("$root/runtime/venv/bin/python" -I -m pip --isolated --disable-pip-version-check install --no-cache-dir --only-binary=:all: --retries 2 --timeout 30)
if [[ "$OPS_SOURCE" == ONLINE ]]; then
  runuser -u "$service_user" -- "${pip[@]}" --index-url https://pypi.org/simple -r "$root/runtime/source/requirements.txt"
else
  runuser -u "$service_user" -- "${pip[@]}" --no-index --find-links "$root/runtime/wheelhouse" -r "$root/runtime/source/requirements.txt"
fi
runuser -u "$service_user" -- "$root/runtime/venv/bin/python" -I -m pip --isolated --disable-pip-version-check check
runuser -u "$service_user" -- "$root/runtime/venv/bin/python" -I -c 'import cv2, flask, flask_socketio, simple_websocket; print("Python 依赖已就绪")'
chown -hR root:"$group" "$root/runtime/venv"
find "$root/runtime/venv" -type d -exec chmod g+rx,o-rwx {} +
find "$root/runtime/venv" -type f -exec chmod g+r,o-rwx {} +
