# SFOPS_CHECK_HOST_TERMINAL
set -eu -o pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C
. /etc/os-release
[[ -n ${ID:-} ]] || exit 1
if [[ $ID != ubuntu || ${VERSION_ID:-} != 24.04 ]]; then
    [[ $ID != ubuntu || -n ${VERSION_ID:-} ]] || exit 1
    echo SFOPS_TERMINAL_NOT_APPLICABLE
    exit 0
fi
missing() { echo SFOPS_TERMINAL_MISSING; exit 0; }
for dir in /usr /usr/local /usr/local/bin; do
    [[ -d $dir && ! -L $dir ]] || missing
    read -r owner mode < <(stat -c '%u %a' -- "$dir")
    [[ $owner == 0 ]] && (( (8#$mode & 022) == 0 )) || missing
done
# Observe after the installer releases its lock. An in-flight operation is
# indeterminate and should be rechecked instead of being declared absent.
exec 9</usr/local/bin
flock -sn 9 || exit 1
launcher=/usr/local/bin/sf-docker
[[ -f $launcher && ! -L $launcher && $(stat -c '%u %a' -- "$launcher") == '0 755' ]] || missing
digest=$(sha256sum -- "$launcher")
case ${digest%% *} in @EXPECTED_SHA@|@CURRENT_SHA@) ;; *) missing ;; esac
clients=(mysql redis-cli psql curl)
packages=(mysql-client-core-8.0 redis-tools postgresql-client-16 curl)
for i in "${!clients[@]}"; do
    timeout 4 "/usr/bin/${clients[i]}" --version >/dev/null 2>&1 || missing
    state=$(dpkg-query -W -f='${db:Status-Status}' "${packages[i]}" 2>/dev/null) || continue
    [[ $state == installed || $state == not-installed || $state == config-files ]] || missing
done
echo SFOPS_TERMINAL_READY
