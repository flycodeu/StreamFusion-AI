read_secret() {
  local file="$1" value
  [[ -f "$file" && ! -L "$file" ]] || fail '请选择所需密码或密钥'
  [[ $(wc -l < "$file") -eq 0 ]] || fail '密码或密钥不能包含换行'
  value="$(cat "$file")"
  [[ -n "$value" && "$value" != *$'\n'* && "$value" != *$'\r'* ]] || fail '密码或密钥不能为空或包含换行'
  printf '%s' "$value"
}
