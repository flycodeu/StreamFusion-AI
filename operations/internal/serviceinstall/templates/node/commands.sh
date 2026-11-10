set -e
umask 077
cd -- "$HOME"
action=$1
root=$2
version=$3
package=$4
material=$5
job=$6
default=$7
configs=$8
NVM_DIR="$root/nvm"
external=false
if [ "$action" = inspect-external ]; then NVM_DIR="$root"; action=inspect; external=true; fi
export NVM_DIR
export NVM_NO_COLORS=1
export NPM_CONFIG_USERCONFIG="$configs/user.npmrc" NPM_CONFIG_GLOBALCONFIG="$configs/global.npmrc"
export NPM_CONFIG_IGNORE_SCRIPTS=true NPM_CONFIG_AUDIT=false NPM_CONFIG_FUND=false
export NPM_CONFIG_CACHE="$root/cache/$version/npm"
export NPM_CONFIG_REGISTRY=https://registry.npmjs.org/

fail() { printf '%s\n' "$1" >&2; exit 1; }
load_nvm() { [ -f "$NVM_DIR/nvm.sh" ] && [ ! -L "$NVM_DIR/nvm.sh" ] || fail NODE_MANAGER_MISSING; . "$NVM_DIR/nvm.sh" --no-use; }
check_tree() {
  current=$1
  while [ "$current" != / ]; do
    [ ! -L "$current" ] || fail NODE_PATH_SYMLINK
    current=${current%/*}; [ -n "$current" ] || current=/
  done
}
check_mounts() {
  mounts=$(findmnt -rn -o TARGET) || fail NODE_MOUNT_CHECK_FAILED
  if printf '%s\n' "$mounts" | awk -v p="$1" '$0==p || index($0,p"/")==1 {found=1} END {exit !found}'; then fail NODE_NESTED_MOUNT; fi
}
check_tree "$root"
check_tree "$NVM_DIR"
check_tree "$NVM_DIR/alias"
check_tree "$NVM_DIR/.cache"
check_tree "$root/cache"
check_tree "$root/backups"

inspect_version() {
  base=$1
  [ -x "$base/bin/node" ] && [ ! -L "$base/bin/node" ] || fail NODE_BINARY_MISSING
  nv=$("$base/bin/node" --version)
  bh=$(sha256sum -- "$base/bin/node"); bh=${bh%% *}
  npm=""; pnpm=""
  if [ -f "$base/lib/node_modules/npm/bin/npm-cli.js" ]; then npm=$("$base/bin/node" "$base/lib/node_modules/npm/bin/npm-cli.js" --version); fi
  if [ -f "$base/lib/node_modules/pnpm/bin/pnpm.cjs" ]; then pnpm=$(PATH="$base/bin:/usr/bin:/bin" "$base/bin/node" "$base/lib/node_modules/pnpm/bin/pnpm.cjs" --version); fi
  printf 'VERSION\t%s\t%s\t%s\t%s\t%s\n' "$nv" "$base/bin/node" "$bh" "$npm" "$pnpm"
  "$base/bin/node" -e 'const fs=require("fs"),p=require("path"),base=process.argv[1],v=process.version;let n=0;for(const a of fs.readdirSync(base)){const names=a.startsWith("@")?fs.readdirSync(p.join(base,a)).map(b=>a+"/"+b):[a];for(const name of names){if(++n>256)throw Error("NODE_PACKAGE_LIMIT");try{const m=JSON.parse(fs.readFileSync(p.join(base,name,"package.json"),"utf8"));if(/^[a-zA-Z0-9@/_.-]+$/.test(m.name)&&/^[a-zA-Z0-9.+_-]+$/.test(m.version))console.log(["PACKAGE",v,m.name,m.version].join("\t"));}catch(e){if(e.code!=="ENOENT")throw e;}}}' "$base/lib/node_modules"
}

case "$action" in
  inspect-system)
    if [ ! -x /usr/bin/node ]; then printf 'ABSENT\n'; exit 0; fi
    nv=$(/usr/bin/node --version)
    bh=$(sha256sum /usr/bin/node); bh=${bh%% *}
    npm=""; if [ -x /usr/bin/npm ]; then npm=$(/usr/bin/npm --version); fi
    printf 'SYSTEM\nDEFAULT\t%s\nVERSION\t%s\t/usr/bin/node\t%s\t%s\t\n' "$nv" "$nv" "$bh" "$npm"
    ;;
  inspect)
    if [ ! -e "$root" ] || [ ! -e "$NVM_DIR/nvm.sh" ]; then printf 'ABSENT\n'; exit 0; fi
    load_nvm
    nh=$(sha256sum -- "$NVM_DIR/nvm.sh"); nh=${nh%% *}
    eh=""
    if [ -f "$NVM_DIR/nvm-exec" ] && [ ! -L "$NVM_DIR/nvm-exec" ]; then eh=$(sha256sum -- "$NVM_DIR/nvm-exec"); eh=${eh%% *}; fi
    printf 'NVM\t%s\t%s\t%s\n' "$(nvm --version)" "$nh" "$eh"
    actual_default=""
    if [ -e "$NVM_DIR/alias/default" ]; then [ ! -L "$NVM_DIR/alias/default" ] || fail NODE_DEFAULT_SYMLINK; actual_default=$(cat "$NVM_DIR/alias/default"); fi
    if [ "$external" = true ] && [ -n "$actual_default" ]; then actual_default=$(nvm version default || true); [ "$actual_default" != N/A ] || actual_default=""; fi
    printf 'DEFAULT\t%s\n' "$actual_default"
    count=0
    for base in "$NVM_DIR"/versions/node/v*; do
      [ -e "$base" ] || continue
      count=$((count+1)); [ "$count" -le 64 ] || fail NODE_VERSION_LIMIT
      check_tree "$base"; inspect_version "$base"
    done
    ;;
  initialize)
    [ ! -e "$root" ] || fail NODE_DIRECTORY_EXISTS
    mkdir -p -- "$NVM_DIR" "$root/cache" "$root/backups"
    install -m 600 -- "$material/nvm.sh" "$NVM_DIR/nvm.sh"
    install -m 700 -- "$material/nvm-exec" "$NVM_DIR/nvm-exec"
    ;;
  prepare-install)
    check_tree "$NVM_DIR/.cache/bin/node-$version-linux-x64"
    check_tree "$NVM_DIR/versions/node/$version"
    [ ! -e "$NVM_DIR/versions/node/$version" ] || fail NODE_ALREADY_INSTALLED
    ;;
  install)
    load_nvm
    [ ! -e "$NVM_DIR/versions/node/$version" ] || fail NODE_ALREADY_INSTALLED
    cache="$NVM_DIR/.cache/bin/node-$version-linux-x64"
    check_tree "$cache"
    mkdir -p -- "$cache"
    cp -- "$material" "$cache/node-$version-linux-x64.tar.xz"
    nvm install --offline -b --skip-default-packages "$version"
    if [ -n "$default" ]; then nvm alias default "$default"; else rm -f -- "$NVM_DIR/alias/default"; fi
    ;;
  set-default)
    load_nvm
    [ -x "$NVM_DIR/versions/node/$version/bin/node" ] || fail NODE_VERSION_MISSING
    nvm alias default "$version"
    ;;
  update-nvm)
    load_nvm
    mkdir -p -- "$root/backups/nvm/$job"
    cp -- "$NVM_DIR/nvm.sh" "$NVM_DIR/nvm-exec" "$root/backups/nvm/$job/"
    install -m 600 -- "$material/nvm.sh" "$NVM_DIR/nvm.sh.new"
    install -m 700 -- "$material/nvm-exec" "$NVM_DIR/nvm-exec.new"
    mv -- "$NVM_DIR/nvm.sh.new" "$NVM_DIR/nvm.sh"
    mv -- "$NVM_DIR/nvm-exec.new" "$NVM_DIR/nvm-exec"
    ;;
  package-install|package-remove)
    load_nvm
    base="$NVM_DIR/versions/node/$version"
    check_tree "$base"
    [ -x "$base/bin/node" ] || fail NODE_VERSION_MISSING
    check_tree "$base/lib/node_modules"
    backup="$root/backups/$version/$job"
    check_tree "$backup"
    mkdir -p -- "$backup"
    if [ -d "$base/lib/node_modules/$package" ]; then cp -a -- "$base/lib/node_modules/$package" "$backup/$package"; fi
    if [ "$action" = package-install ]; then
      PATH="$base/bin:/usr/bin:/bin" "$base/bin/node" "$base/lib/node_modules/npm/bin/npm-cli.js" install --global --offline --ignore-scripts --no-audit --no-fund --prefix "$base" -- "$material"
    else
      PATH="$base/bin:/usr/bin:/bin" "$base/bin/node" "$base/lib/node_modules/npm/bin/npm-cli.js" uninstall --global --offline --ignore-scripts --no-audit --no-fund --prefix "$base" -- pnpm
    fi
    ;;
  uninstall)
    load_nvm
    base="$NVM_DIR/versions/node/$version"
    check_tree "$base"
    for cache_files in "$NVM_DIR/.cache/bin/node-$version-linux-x64/files" "$NVM_DIR/.cache/src/node-$version/files"; do
      check_tree "$cache_files"
      check_mounts "$cache_files"
    done
    # Never traverse another filesystem while removing this exact version.
    check_mounts "$base"
    for proc in /proc/[0-9]*/exe; do
      resolved=$(readlink "$proc" 2>/dev/null || true)
      case "$resolved" in "$base"/*) fail NODE_VERSION_RUNNING;; esac
    done
    if [ "$default" = clear ]; then rm -f -- "$NVM_DIR/alias/default"; fi
    nvm uninstall "$version"
    ;;
  clean-version)
    for dir in "$root/cache/$version" "$root/backups/$version" "$NVM_DIR/.cache/bin/node-$version-linux-x64"; do
      check_tree "$dir"
      check_mounts "$dir"
      rm -rf -- "$dir"
    done
    ;;
  remove-environment)
    load_nvm
    for base in "$NVM_DIR"/versions/node/v*; do [ ! -e "$base" ] || fail NODE_ENVIRONMENT_NOT_EMPTY; done
    check_mounts "$root"
    if [ "$default" = delete-data ]; then rm -rf -- "$root"; else rm -f -- "$NVM_DIR/nvm.sh" "$NVM_DIR/nvm-exec"; fi
    ;;
  *) fail NODE_ACTION_INVALID;;
esac
