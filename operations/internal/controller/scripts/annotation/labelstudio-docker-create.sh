check_owner
chown 1001:0 "$root/data"
chmod 750 "$root/data"
args=(run --detach --name "$container" --label "streamfusion.ops.software-id=$OPS_SOFTWARE_ID" --restart unless-stopped --memory "${OPS_MEMORY_MIB}m" -p "$OPS_BIND_ADDRESS:$OPS_HOST_PORT:8080" -v "$root/data:/label-studio/data" --entrypoint /label-studio/.venv/bin/label-studio)
args+=(-e LABEL_STUDIO_BASE_DATA_DIR=/label-studio/data -e "DATA_UPLOAD_MAX_MEMORY_SIZE=$((OPS_PARAM_MAX_UPLOAD_MIB*1024*1024))" -e "LABEL_STUDIO_HOST=$OPS_PARAM_PUBLIC_URL")
if [[ -n "$OPS_PARAM_SOURCE_DIRECTORY" ]]; then
  args+=(-v "$OPS_PARAM_SOURCE_DIRECTORY:/datasets:ro" -e LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED=true -e LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT=/datasets)
else
  args+=(-e LABEL_STUDIO_LOCAL_FILES_SERVING_ENABLED=false -e LABEL_STUDIO_LOCAL_FILES_DOCUMENT_ROOT=/label-studio/data)
fi
docker "${args[@]}" '@@IMAGE_ID@@' start --no-browser --internal-host 0.0.0.0 --port 8080
if [[ -n "$OPS_PARAM_SOURCE_DIRECTORY" ]]; then
  docker exec --user 1001 "$container" /bin/sh -c 'test -r /datasets && test -x /datasets' || fail '容器账号不能读取数据集目录，请由管理员授予 UID 1001 读取权限'
fi
printf '%s\n' 'Label Studio 已启动，请在打开的界面完成账号设置'
