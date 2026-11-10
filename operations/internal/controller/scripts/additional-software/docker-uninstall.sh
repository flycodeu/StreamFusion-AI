check_owner
owned_container
docker stop --time 90 "$container"
docker rm "$container"
printf '%s\n' '容器已移除，数据与配置已保留'
