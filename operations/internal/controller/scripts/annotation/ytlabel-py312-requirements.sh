"$python" -I - "$root/runtime/source/requirements.txt" "$root/config" <<'PY'
@@REQUIREMENTS@@
PY
printf '%s\n' '依赖来源：https://pypi.org/simple；离线安装使用本次材料中的 wheelhouse，依赖版本保持一致。'
