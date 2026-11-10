export NVM_DIR="$OPS_NODE_ROOT/nvm"
. "$NVM_DIR/nvm.sh" --no-use
if [ -n "$OPS_NODE_DEFAULT" ]; then nvm alias default "$OPS_NODE_DEFAULT"; else rm -f -- "$NVM_DIR/alias/default"; fi