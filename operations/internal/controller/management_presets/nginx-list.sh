printf '{"columns":["站点","状态","端口"],"rows":['
separator=''
count=0
for file in "$sites"/*.conf "$sites"/*.conf.disabled; do
  [[ -f "$file" && ! -L "$file" ]] || continue
  name=${file##*/}; name=${name%.disabled}; name=${name%.conf}
  [[ "$name" =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,47}$ ]] || continue
  count=$((count+1)); [[ "$count" -le 200 ]] || continue
  state=启用; [[ "$file" != *.disabled ]] || state=停用
  port=$(sed -n 's/^# ops-port \([0-9]*\)$/\1/p' "$file" | head -n 1)
  printf '%s{"站点":"%s","状态":"%s","端口":"%s"}' "$separator" "$name" "$state" "$port"
  separator=,
done
printf '],"total":%s}\n' "$count"
