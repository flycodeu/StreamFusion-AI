for binary in redis-server redis-cli; do
  libraries=$(/lib64/ld-linux-x86-64.so.2 --library-path "$OPS_INSTALL_ROOT/runtime/lib" --list "$OPS_INSTALL_ROOT/runtime/redis/$binary")
  case "$libraries" in *'not found'*) printf '%s\n' "$libraries"; exit 1;; esac
done
