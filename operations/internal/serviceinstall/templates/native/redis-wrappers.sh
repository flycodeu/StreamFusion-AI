
for binary in redis-server redis-cli; do
  printf '#!/bin/sh\nexec /lib64/ld-linux-x86-64.so.2 --argv0 %s --library-path %s/runtime/lib %s/runtime/redis/%s "$@"\n' "$binary" "$OPS_INSTALL_ROOT" "$OPS_INSTALL_ROOT" "$binary" > "$OPS_INSTALL_ROOT/bin/$binary"
  chmod 755 "$OPS_INSTALL_ROOT/bin/$binary"
done