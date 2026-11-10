
tool=$1
[ "$(/usr/bin/id -u)" != 0 ] || exit 1
entry=$(/usr/bin/getent passwd "$(/usr/bin/id -u)") || exit 1
HOME=$(printf '%s\n' "$entry" | /usr/bin/cut -d: -f6)
case "$HOME" in /*) [ "$HOME" != / ] || exit 1;; *) exit 1;; esac
export HOME DISABLE_AUTOUPDATER=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
for binary in "$HOME/.local/bin/$tool" /usr/local/bin/"$tool" /usr/bin/"$tool" "$HOME/.npm-global/bin/$tool" "$HOME"/.nvm/versions/node/*/bin/"$tool"; do
  [ -f "$binary" ] && [ -x "$binary" ] || continue
  PATH="${binary%/*}:/usr/local/bin:/usr/bin:/bin" "$binary" --version
  exit $?
done
exit 1
