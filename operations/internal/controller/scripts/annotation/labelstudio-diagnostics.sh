check_owner
if [[ "$OPS_RUNTIME_KIND" == DOCKER ]]; then
  docker inspect --format 'Label Studio: {{.State.Status}}' "$OPS_SOFTWARE_LOCATOR"
else
  systemctl show --property=ActiveState,SubState --no-pager "$OPS_SOFTWARE_LOCATOR"
fi
