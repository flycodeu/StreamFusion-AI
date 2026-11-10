root="$OPS_NODE_ROOT"
[ ! -L "$root" ] || exit 1
rm -rf -- "$root"