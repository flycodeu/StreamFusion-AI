for binary in "$OPS_INSTALL_ROOT/runtime/git/usr/bin/git" "$OPS_INSTALL_ROOT/runtime/git/usr/lib/git-core/git-remote-http"; do
  libraries=$(/usr/bin/ldd "$binary")
  case "$libraries" in *'not found'*) printf '%s\n' "$libraries"; exit 1;; esac
done
