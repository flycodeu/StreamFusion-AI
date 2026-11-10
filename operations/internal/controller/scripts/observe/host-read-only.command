/bin/sh -c '
printf "SFOPS_HOST_V1\n"; uname -s; uname -m
(
if command -v docker >/dev/null 2>&1 || command -v dockerd >/dev/null 2>&1; then
  printf "DOCKER_PRESENT\n"; exit 0
fi
unknown=0
for dir in /bin /sbin /usr/bin /usr/sbin /usr/local/bin /usr/local/sbin /snap/bin; do
  for name in docker dockerd; do
    if [ -e "$dir/$name" ] || [ -L "$dir/$name" ]; then printf "DOCKER_PRESENT\n"; exit 0; fi
  done
  current=$dir
  while [ ! -e "$current" ] && [ ! -L "$current" ]; do
    current=${current%/*}; current=${current:-/}
  done
  if [ ! -d "$current" ] || [ ! -r "$current" ] || [ ! -x "$current" ]; then unknown=1; fi
done
for unit in docker.service snap.docker.dockerd.service; do
  if state=$(systemctl show "$unit" --property=LoadState --value 2>/dev/null); then
    case "$state" in
      not-found) ;;
      loaded|masked|bad-setting|error|merged) printf "DOCKER_PRESENT\n"; exit 0 ;;
      *) unknown=1 ;;
    esac
  else
    unknown=1
  fi
done
if [ "$unknown" = 0 ]; then printf "DOCKER_MISSING\n"; else printf "DOCKER_UNKNOWN\n"; fi
)
if [ -r /etc/os-release ]; then
  /usr/bin/awk -F= "/^ID=/{id=\$2} /^VERSION_ID=/{version=\$2} END{gsub(/\"/, \"\", id); gsub(/\"/, \"\", version); printf \"DISTRO %s %s\\n\", id, version}" /etc/os-release
fi'