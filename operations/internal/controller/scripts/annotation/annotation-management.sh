annotation_manage() {
  local interpreter program
  program=$(cat)
  if [[ -f "$OPS_INSTALL_ROOT/config/python-binary" ]]; then
    interpreter=$(cat "$OPS_INSTALL_ROOT/config/python-binary")
  elif [[ -x "$OPS_INSTALL_ROOT/runtime/venv/bin/python" ]]; then
    interpreter="$OPS_INSTALL_ROOT/runtime/venv/bin/python"
  else
    interpreter=$(command -v python3) || { printf '%s\n' '配置管理需要系统 Python 3'; return 1; }
  fi
  {
    cat <<'PY'
@@COMMON@@
@@APPLICATION@@
PY
    printf '\n%s\n' "$program"
  } | "$interpreter" -I -S - "$OPS_INSTALL_ROOT" '@@APP@@' '@@MODE@@' "$@"
}
