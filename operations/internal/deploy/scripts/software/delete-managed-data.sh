set -euo pipefail
directory=$1
if [[ "$2" == empty ]]; then
  /usr/bin/rmdir -- "$directory"
  exit
fi
[[ -d "$directory" && ! -L "$directory" && -f "$directory/.streamfusion-ops-data.json" && ! -L "$directory/.streamfusion-ops-data.json" ]]
# Keep the ownership marker until all other entries have been removed.
/usr/bin/find -P "$directory" -xdev -mindepth 1 -depth ! -path "$directory/.streamfusion-ops-data.json" -delete
/usr/bin/rm -- "$directory/.streamfusion-ops-data.json"
/usr/bin/rmdir -- "$directory"
