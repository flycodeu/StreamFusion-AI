set -eu
umask 077
action=$1; directory=$2; stage=$3; uid=$4; method=$5; key=$6; helper=$7; version=$8
identity_key=${9}; with_identity=${10}
parent=$HOME
for part in .local share streamfusion-ops git-accounts "${directory##*/}"; do
  parent="$parent/$part"
  if [ ! -e "$parent" ]; then mkdir -m 700 -- "$parent"; fi
  [ -d "$parent" ] && [ ! -L "$parent" ] && [ "$(stat -c %u -- "$parent")" = "$uid" ] || exit 1
  permissions=$(stat -c %a -- "$parent")
  [ "$((8#$permissions & 8#22))" -eq 0 ] || exit 1
done
exec 9>"$directory/.lock"
flock -w 5 9
for name in credentials ssh.conf revision identity.gitconfig known_hosts; do
  [ ! -L "$directory/$name" ] || exit 1
done
global_config="$HOME/.gitconfig"
[ ! -L "$global_config" ] || exit 1
if [ "$action" = GIT_ACCOUNT_CONFIGURE ] && { [ "$method" = HTTPS ] || [ "$with_identity" = yes ]; } && [ ! -e "$global_config" ]; then
  # Git preserves an existing mode, while a new Git lock inherits default ACLs.
  temporary_global=$(mktemp "$HOME/.ops-gitconfig.XXXXXXXX")
  trap 'rm -f -- "$temporary_global"' EXIT
  chmod 600 -- "$temporary_global"
  ln -T -- "$temporary_global" "$global_config"
  rm -f -- "$temporary_global"
  trap - EXIT
fi
if [ -e "$global_config" ]; then
  [ -f "$global_config" ] && [ "$(stat -c %u -- "$global_config")" = "$uid" ] || exit 1
  permissions=$(stat -c %a -- "$global_config")
  [ "$((8#$permissions & 8#22))" -eq 0 ] || exit 1
fi
if [ -f "$directory/revision" ]; then
  saved=$(cat -- "$directory/revision")
  case "$saved" in ''|*[!0-9]*) exit 1;; esac
  [ "$saved" -le "$version" ] || exit 1
fi
printf '%s\n' "$version" > "$directory/revision"
if [ "$method" = HTTPS ]; then
  existing=$(/usr/bin/git config --global --get-all "$key" || true)
  [ -z "$existing" ] || [ "$existing" = "$helper" ] || exit 1
  if [ "$action" = GIT_ACCOUNT_REMOVE ]; then
    if [ -n "$existing" ]; then /usr/bin/git config --global --unset-all "$key"; fi
    rm -f -- "$directory/credentials" "$directory/revision"
  else
    /usr/bin/git credential-store --file "$directory/credentials" store < "$stage/credential"
    chmod 600 -- "$directory/credentials"
    if [ -z "$existing" ]; then /usr/bin/git config --global --add "$key" "$helper"; fi
    printf '%s\n' "$version" > "$directory/revision"
  fi
else
  if [ ! -e "$HOME/.ssh" ]; then mkdir -m 700 -- "$HOME/.ssh"; fi
  [ -d "$HOME/.ssh" ] && [ ! -L "$HOME/.ssh" ] && [ "$(stat -c %u -- "$HOME/.ssh")" = "$uid" ] || exit 1
  config="$HOME/.ssh/config"
  [ ! -L "$config" ] || exit 1
  include="Include \"$directory/ssh.conf\""
  temporary=$(mktemp "$HOME/.ssh/.ops-config.XXXXXXXX")
  trap 'rm -f -- "$temporary"' EXIT
  if [ "$action" = GIT_ACCOUNT_REMOVE ]; then
    if [ -f "$config" ]; then while IFS= read -r line || [ -n "$line" ]; do [ "$line" = "$include" ] || printf '%s\n' "$line"; done < "$config" > "$temporary"; fi
    rm -f -- "$directory/ssh.conf" "$directory/revision" "$directory/known_hosts"
  else
    install -m 600 -- "$stage/ssh.conf" "$directory/ssh.conf"
    if [ -f "$stage/known_hosts" ]; then install -m 600 -- "$stage/known_hosts" "$directory/known_hosts"; fi
    printf '%s\n' "$include" > "$temporary"
    if [ -f "$config" ]; then while IFS= read -r line || [ -n "$line" ]; do [ "$line" = "$include" ] || printf '%s\n' "$line"; done < "$config" >> "$temporary"; fi
    printf '%s\n' "$version" > "$directory/revision"
  fi
  chmod 600 -- "$temporary"
  mv -T -- "$temporary" "$config"
fi
identity_path="$directory/identity.gitconfig"
previous_identity=$(/usr/bin/git config --global --get-all "$identity_key" || true)
[ -z "$previous_identity" ] || [ "$previous_identity" = "$identity_path" ] || exit 1
if [ "$action" = GIT_ACCOUNT_REMOVE ] || [ "$with_identity" = no ]; then
  if [ -n "$previous_identity" ]; then /usr/bin/git config --global --unset-all "$identity_key"; fi
  rm -f -- "$identity_path"
else
  install -m 600 -- "$stage/identity.gitconfig" "$identity_path"
  if [ -z "$previous_identity" ]; then /usr/bin/git config --global --add "$identity_key" "$identity_path"; fi
fi
