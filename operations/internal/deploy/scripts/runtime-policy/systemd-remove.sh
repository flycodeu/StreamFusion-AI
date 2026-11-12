set -euo pipefail
unit=$1; marker=$2
directory="/etc/systemd/system/$unit.d"
file="$directory/90-streamfusion-ops-runtime.conf"
for parent in /etc /etc/systemd /etc/systemd/system; do
  [[ -d "$parent" && ! -L "$parent" && $(stat -c %u "$parent") == 0 ]]
  [[ $(( 8#$(stat -c %a "$parent") & 0022 )) == 0 ]]
done
[[ ! -L "$directory" && ! -L "$file" ]]
if [[ -e "$file" ]]; then
  [[ -d "$directory" && $(stat -c %u "$directory") == 0 ]]
  [[ $(( 8#$(stat -c %a "$directory") & 0022 )) == 0 ]]
  [[ -f "$file" && $(stat -c %u "$file") == 0 ]]
  [[ $(( 8#$(stat -c %a "$file") & 0022 )) == 0 ]]
  [[ $(head -n 1 "$file") == "# StreamFusion Ops runtime $marker" ]]
  rm -- "$file"
fi
systemctl daemon-reload
