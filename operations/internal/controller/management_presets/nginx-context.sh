set -Eeuo pipefail
umask 077
official=@OFFICIAL@
sites="$OPS_CONFIG_ROOT/ops-sites"
main="$OPS_CONFIG_ROOT/nginx.conf"
if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
  nginx_test() { docker exec "$OPS_SOFTWARE_LOCATOR" nginx -t; }
  nginx_reload() { docker exec "$OPS_SOFTWARE_LOCATOR" nginx -s reload; }
elif [[ "$official" == true ]]; then
  nginx_test() { /usr/sbin/nginx -t; }
  nginx_reload() { systemctl reload "$OPS_SOFTWARE_LOCATOR"; }
else
  nginx_test() { "$OPS_INSTALL_ROOT/bin/nginx" -p "$OPS_INSTALL_ROOT" -c "$main" -t; }
  nginx_reload() { "$OPS_INSTALL_ROOT/bin/nginx" -p "$OPS_INSTALL_ROOT" -c "$main" -s reload; }
fi
