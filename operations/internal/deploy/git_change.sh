set -eu
umask 077
action=$1
repo=$2
uid=$3
before=$4
after=$5
mode=$6
stage=$7
private=$8
job=$9
previous_key=${10}
previous_known=${11}
config="$repo/.git/config"
check_path() {
  p=$1
  while [ "$p" != / ]; do
    [ ! -L "$p" ] || exit 1
    p=${p%/*}; p=${p:-/}
  done
}
check_hash() { actual=$(sha256sum -- "$1"); [ "${actual%% *}" = "$2" ]; }
check_config() {
  check_path "$config"
  [ -f "$config" ] && [ "$(stat -c %u -- "$config")" = "$uid" ] || exit 1
  [ "$(stat -c %a -- "$config")" = "$mode" ] || exit 1
  check_hash "$config" "$before"
}
check_config
check_path "$private"
if [ -n "$previous_key" ]; then
  check_hash "$private/key" "$previous_key"
  check_hash "$private/known_hosts" "$previous_known"
fi
check_hash "$stage/config" "$after"
directory=$HOME
for part in .local share streamfusion-ops git "${private##*/}"; do
  directory="$directory/$part"
  if [ ! -e "$directory" ]; then mkdir -m 700 -- "$directory"; fi
  [ -d "$directory" ] && [ ! -L "$directory" ] || exit 1
  owner=$(stat -c %u -- "$directory")
  [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || exit 1
  permissions=$(stat -c %a -- "$directory")
  [ "$((8#$permissions & 8#22))" -eq 0 ] || exit 1
done
chmod 700 -- "$private"
temporary=$(mktemp "$repo/.git/.sfops-config.XXXXXXXX")
trap 'rm -f -- "$temporary"' EXIT
chmod 600 -- "$temporary"
cat -- "$stage/config" > "$temporary"
chmod "$mode" -- "$temporary"
if [ "$action" = BIND ]; then
  for name in key known_hosts; do
    next="$private/$name.$job"
    [ ! -e "$next" ] && [ ! -L "$next" ] || exit 1
    install -m 600 -- "$stage/$name" "$next"
    mv -T -- "$next" "$private/$name"
  done
  if [ ! -e "$private/original.config" ]; then
    install -m 600 -- "$stage/original.config" "$private/original.config"
  fi
fi
check_config
mv -T -- "$temporary" "$config"
sync -f "$config"
if [ "$action" = UNBIND ]; then
  rm -- "$private/key" "$private/known_hosts"
fi
