
selected_pids=
for unit do
  file=$(/usr/bin/systemctl show --no-pager --property=FragmentPath --value -- "$unit" 2>/dev/null)
  pid=$(/usr/bin/systemctl show --no-pager --property=MainPID --value -- "$unit" 2>/dev/null)
  exe=
  case "$pid" in ''|*[!0-9]*|0) ;; *) exe=$(/usr/bin/readlink -f -- "/proc/$pid/exe" 2>/dev/null);; esac
  printf 'FILE\t%s\t%s\n' "$unit" "$file"
  printf 'EXE\t%s\t%s\n' "$unit" "$exe"
  case "$pid" in ''|*[!0-9]*|0) ;; *)
    selected_pids="${selected_pids}${unit} ${pid}
"
  ;; esac
  case "$unit" in label-studio.service|labelstudio.service|label_studio.service) label_studio_package "$unit";; esac
  case "$file" in /etc/systemd/system/*.service|/lib/systemd/system/*.service|/usr/lib/systemd/system/*.service) ;; *) continue;; esac
  if [ -x /usr/bin/dpkg-query ]; then
    owner=$(/usr/bin/dpkg-query -S -- "$file" 2>/dev/null)
    if [ -z "$owner" ]; then
      alternate=
      case "$file" in /usr/lib/systemd/*) alternate="/lib/${file#/usr/lib/}";; /lib/systemd/*) alternate="/usr/lib/${file#/lib/}";; esac
      if [ -n "$alternate" ] && [ "$(/usr/bin/readlink -f -- "$file" 2>/dev/null)" = "$(/usr/bin/readlink -f -- "$alternate" 2>/dev/null)" ]; then
        owner=$(/usr/bin/dpkg-query -S -- "$alternate" 2>/dev/null)
      fi
    fi
    owner=${owner%%: /*}
    case "$owner" in ''|*[!a-zA-Z0-9+:._-]*) continue;; esac
    /usr/bin/dpkg-query -W '-f=${binary:Package}\t${Version}\t${db:Status-Status}\n' -- "$owner" 2>/dev/null |
      /usr/bin/awk -F '\t' -v unit="$unit" '$3=="installed" {printf "PACKAGE\t%s\t%s\t%s\n",unit,$1,$2}'
  elif [ -x /usr/bin/rpm ]; then
    /usr/bin/rpm -qf --qf '%{NAME}\t%{VERSION}-%{RELEASE}\n' -- "$file" 2>/dev/null |
      /usr/bin/awk -F '\t' -v unit="$unit" 'NF==2 {printf "PACKAGE\t%s\t%s\t%s\n",unit,$1,$2}'
  fi
done
if [ -n "$selected_pids" ]; then
  # Filter on the host before bounding output. Busy hosts can have far more
  # unrelated sockets than the response budget; process arguments stay local.
  /usr/bin/timeout 2s /usr/bin/ss -H -lntup 2>/dev/null |
    /usr/bin/awk -v selected="$selected_pids" '
      BEGIN { count=split(selected, rows, "\n"); for (i=1; i<=count; i++) { split(rows[i], field, " "); units[i]=field[1]; pids[i]=field[2] } }
      $1=="tcp" || $1=="udp" {
        for (i=1; i<=count; i++) if (pids[i]!="" && $0 ~ "pid="pids[i]"[,)]") printf "PORT\t%s\t%s/%s\n",units[i],$5,$1
      }' | /usr/bin/head -c 65537
fi
