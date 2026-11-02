set -eu
umask 077
file=$1
before=$2
after=$3
mode=$4
stage=$5
backup=$6
uid=$7
check_path() {
  current=$1
  while [ "$current" != / ]; do
    [ ! -L "$current" ] || exit 1
    current=${current%/*}; [ -n "$current" ] || current=/
  done
}
check_file() {
  check_path "$file"
  [ "$(stat -c %u -- "$HOME")" = "$uid" ] || exit 1
  if [ -n "$before" ]; then
    [ -f "$file" ] && [ "$(stat -c %u -- "$file")" = "$uid" ] || exit 1
    hash=$(sha256sum -- "$file"); [ "${hash%% *}" = "$before" ] || exit 1
    [ "$(stat -c %a -- "$file")" = "$mode" ] || exit 1
  else
    [ ! -e "$file" ] || exit 1
  fi
}
check_file
hash=$(sha256sum -- "$stage"); [ "${hash%% *}" = "$after" ] || exit 1
check_path "$backup"
mkdir -p -- "$backup"
if [ -n "$before" ]; then
  # Backup is exclusive and never overwrites an earlier attempt.
  (set -C; cat -- "$file" > "$backup/${file##*/}")
fi
temporary=$(mktemp "$HOME/.sfops-node-profile.XXXXXXXX")
trap 'rm -f -- "$temporary"' EXIT
cat -- "$stage" > "$temporary"
chmod "$mode" "$temporary"
check_file
mv -T -- "$temporary" "$file"
sync -f "$file"
