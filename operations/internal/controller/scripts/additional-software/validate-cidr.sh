validate_cidr() {
  local value="$1" part prefix
  [[ "$value" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]] || fail '允许连接的网段请使用 IPv4 CIDR 格式'
  IFS=./ read -r -a parts <<< "$value"
  for part in "${parts[@]:0:4}"; do [[ $((10#$part)) -le 255 ]] || fail '网段的 IPv4 地址超出范围'; done
  prefix="${parts[4]}"; [[ $((10#$prefix)) -le 32 ]] || fail '网段掩码需要在 0 至 32 之间'
}
