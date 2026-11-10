export NVM_DIR="$OPS_NODE_ROOT/nvm"
. "$NVM_DIR/nvm.sh" --no-use
[ ! -e "$NVM_DIR/versions/node/$OPS_NODE_VERSION" ]
nvm install --offline -b --skip-default-packages "$OPS_NODE_VERSION"