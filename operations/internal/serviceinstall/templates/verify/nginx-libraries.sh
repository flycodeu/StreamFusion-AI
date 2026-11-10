libraries=$(/usr/bin/ldd "$OPS_BINARY")
case "$libraries" in *'not found'*) printf '%s\n' "$libraries"; exit 1;; esac
