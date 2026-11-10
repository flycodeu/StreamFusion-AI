
for file in /etc/mysql/my.cnf /etc/mysql/mysql.conf.d/mysqld.cnf /etc/mysql/mariadb.conf.d/50-server.cnf /etc/my.cnf /etc/redis/redis.conf /etc/redis.conf; do
  if [ ! -e "$file" ]; then printf 'MISSING\t%s\n' "$file"; continue; fi
  if [ ! -f "$file" ] || [ ! -r "$file" ]; then printf 'DENIED\t%s\n' "$file"; continue; fi
  size=$(/usr/bin/stat -L -c %s -- "$file" 2>/dev/null)
  case "$size" in ''|*[!0-9]*) printf 'DENIED\t%s\n' "$file"; continue;; esac
  if [ "${#size}" -gt 7 ] || [ "$size" -gt 1048576 ]; then printf 'LIMIT\t%s\n' "$file"; continue; fi
  case "$file" in /etc/redis*) kind=REDIS;; *) kind=MYSQL;; esac
  printf 'FOUND\t%s\n' "$file"
  /usr/bin/awk -v kind="$kind" -v file="$file" '
    /^[[:space:]]*[#;]/ {next}
    /^[[:space:]]*\[/ {section=$0; sub(/[[:space:]]*[#;].*$/,"",section); gsub(/[[:space:]]/,"",section); next}
    { line=$0; sub(/[[:space:]]*[#;].*$/,"",line)
      if (kind=="MYSQL") {
        if(section!="[mysqld]") next
        if(line ~ /^[[:space:]]*port[[:space:]]*=/) { sub(/^[^=]*=[[:space:]]*/,"",line); gsub(/[[:space:]"\047]/,"",line); if(line ~ /^[0-9]+$/ && line+0>=1 && line+0<=65535) printf "PORT\t%s\t%s\n",file,line }
        if(line ~ /^[[:space:]]*bind-address[[:space:]]*=/) { sub(/^[^=]*=[[:space:]]*/,"",line); gsub(/[[:space:]"\047]/,"",line); if(line ~ /^[0-9a-fA-F:.*,-]+$/) printf "BIND\t%s\t%s\n",file,line }
      } else {
        if(line ~ /^[[:space:]]*port[[:space:]]+/) { sub(/^[[:space:]]*port[[:space:]]+/,"",line); gsub(/[[:space:]]/,"",line); if(line ~ /^[0-9]+$/ && line+0>=1 && line+0<=65535) printf "PORT\t%s\t%s\n",file,line }
        if(line ~ /^[[:space:]]*bind[[:space:]]+/) { sub(/^[[:space:]]*bind[[:space:]]+/,"",line); gsub(/[[:space:]]+/,",",line); if(line ~ /^[0-9a-fA-F:.*,-]+$/) printf "BIND\t%s\t%s\n",file,line }
      }
    }' "$file" || printf 'DENIED\t%s\n' "$file"
done
