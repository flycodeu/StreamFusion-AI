label_studio_package() (
  # ExecStart may contain credentials. Inspect it locally and emit only the
  # package name/version read beneath the one concrete launcher environment.
  start=$(/usr/bin/timeout 2s /usr/bin/systemctl show --no-pager --property=ExecStart --value -- "$1" 2>/dev/null | /usr/bin/head -c 16385)
  [ "${#start}" -le 16384 ] || return
  case "$start" in *'argv[]='*) ;; *) return;; esac
  arguments=${start#*'argv[]='}
  arguments=${arguments%%' ;'*}
  launcher= count=0
  set -f
  for argument in $arguments; do
    count=$((count + 1))
    [ "$count" -le 64 ] || return
    case "$argument" in
      /*/bin/label-studio|/*/bin/labelstudio|/*/bin/label_studio)
        [ -z "$launcher" ] || return
        case "$argument" in *[!a-zA-Z0-9_./+-]*|*/../*|*/./*|*//*) return;; esac
        launcher=$argument
        ;;
    esac
  done
  set +f
  [ -n "$launcher" ] && [ -f "$launcher" ] && [ ! -L "$launcher" ] || return
  [ "$(/usr/bin/readlink -f -- "$launcher" 2>/dev/null)" = "$launcher" ] || return
  prefix=${launcher%/bin/*}
  case "$prefix" in ''|/|/proc/*|/sys/*|/dev/*|/run/*) return;; esac
  count=0 version=
  for metadata in "$prefix"/lib/python3.[0-9]*/site-packages/label_studio-[0-9]*.dist-info/METADATA; do
    [ -e "$metadata" ] || continue
    count=$((count + 1))
    # Multiple dist-info directories are ambiguous; do not guess which one ran.
    [ "$count" -eq 1 ] && [ -f "$metadata" ] && [ ! -L "$metadata" ] || return
    [ "$(/usr/bin/readlink -f -- "$metadata" 2>/dev/null)" = "$metadata" ] || return
    size=$(/usr/bin/stat -c '%s' -- "$metadata" 2>/dev/null)
    case "$size" in ''|*[!0-9]*) return;; esac
    [ "$size" -le 65536 ] || return
    content=$(/usr/bin/head -c 65537 -- "$metadata" 2>/dev/null)
    [ "${#content}" -le 65536 ] || return
    version=$(printf '%s\n' "$content" | /usr/bin/awk '
      /^Name: / { name=substr($0,7); sub(/\r$/,"",name); name=tolower(name); gsub(/_/,"-",name); names++ }
      /^Version: / { version=substr($0,10); sub(/\r$/,"",version); versions++ }
      END { if (names==1 && versions==1 && name=="label-studio" && length(version)<=80 && version ~ /^[0-9][0-9A-Za-z.+_-]*$/) printf "%s",version }
    ' 2>/dev/null)
    [ -n "$version" ] || return
  done
  [ "$count" -eq 1 ] && [ -n "$version" ] || return
  printf 'PACKAGE\t%s\tlabel-studio\t%s\n' "$1" "$version"
)
