set -euo pipefail
unit=$1; marker=$2; restart=$3; delay=$4; timeout=$5
directory="/etc/systemd/system/$unit.d"
file="$directory/90-streamfusion-ops-runtime.conf"
for parent in /etc /etc/systemd /etc/systemd/system; do
  [[ -d "$parent" && ! -L "$parent" && $(stat -c %u "$parent") == 0 ]]
  [[ $(( 8#$(stat -c %a "$parent") & 0022 )) == 0 ]]
done
[[ ! -L "$directory" && ! -L "$file" ]]
if [[ -e "$directory" ]]; then
  [[ -d "$directory" && $(stat -c %u "$directory") == 0 ]]
  [[ $(( 8#$(stat -c %a "$directory") & 0022 )) == 0 ]]
else
  install -d -o root -g root -m 755 "$directory"
fi
if [[ -e "$file" ]]; then
  [[ -f "$file" && $(stat -c %u "$file") == 0 ]]
  [[ $(( 8#$(stat -c %a "$file") & 0022 )) == 0 ]]
  [[ $(head -n 1 "$file") == "# StreamFusion Ops runtime $marker" ]]
fi
temporary=$(mktemp "$directory/.ops-runtime.XXXXXXXX")
trap 'rm -f -- "$temporary"' EXIT
printf '# StreamFusion Ops runtime %s\n[Service]\nRestart=%s\nRestartSec=%ss\n' "$marker" "$restart" "$delay" > "$temporary"
if [[ "$timeout" != 0 ]]; then printf 'TimeoutStopSec=%ss\n' "$timeout" >> "$temporary"; fi
chmod 644 "$temporary"
mv -T -- "$temporary" "$file"
systemctl daemon-reload
