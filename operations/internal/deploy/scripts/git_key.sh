set -eu
umask 077
id=$1
uid=$2
generate=$3
directory=$HOME
for part in .local share streamfusion-ops git-keys; do
  directory="$directory/$part"
  if [ ! -e "$directory" ] && [ "$generate" = yes ]; then mkdir -m 700 -- "$directory"; fi
  [ -d "$directory" ] && [ ! -L "$directory" ] || exit 1
  [ "$(stat -c %u -- "$directory")" = "$uid" ] || exit 1
  permissions=$(stat -c %a -- "$directory")
  [ "$((8#$permissions & 8#22))" -eq 0 ] || exit 1
done
keydir="$directory/$id"
if [ ! -e "$keydir" ] && [ ! -L "$keydir" ] && [ "$generate" = yes ]; then
  stage=$(mktemp -d "$directory/.new.XXXXXXXX")
  trap 'rm -f -- "$stage/key" "$stage/key.pub"; rmdir -- "$stage"' EXIT
  /usr/bin/ssh-keygen -q -t ed25519 -N '' -C "ops-git-$id" -f "$stage/key"
  # A default ACL can override umask. Tighten only this newly generated key
  # inside the private staging directory before publishing the identity.
  chmod 600 -- "$stage/key"
  mv -T -n -- "$stage" "$keydir"
  if [ ! -e "$stage" ]; then trap - EXIT; fi
fi
[ -d "$keydir" ] && [ ! -L "$keydir" ] && [ "$(stat -c %u -- "$keydir")" = "$uid" ] || exit 1
[ "$(stat -c %a -- "$keydir")" = 700 ] || exit 1
key="$keydir/key"
[ -f "$key" ] && [ ! -L "$key" ] && [ "$(stat -c %u -- "$key")" = "$uid" ] || exit 1
[ "$(stat -c %a -- "$key")" = 600 ] && [ "$(stat -c %s -- "$key")" -le 32768 ] || exit 1
/usr/bin/ssh-keygen -y -P '' -f "$key"
