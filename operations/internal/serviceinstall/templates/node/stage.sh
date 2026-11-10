cache="$OPS_NODE_ROOT/nvm/.cache/bin/node-$OPS_NODE_VERSION-linux-x64"
[ ! -L "$cache" ]
mkdir -p -- "$cache"
cp -- "$OPS_NODE_MATERIAL" "$cache/node-$OPS_NODE_VERSION-linux-x64.tar.xz"