need docker
need curl
[[ "$OPS_MEMORY_MIB" -ge @@MEMORY@@ ]] || fail '此软件至少需要 @@MEMORY@@ MiB 安装内存'
docker info --format '{{.OSType}}' | grep -qx linux || fail 'Docker 服务不可用'
if [[ "$OPS_SOURCE" == REGISTRY ]]; then
  docker pull --platform linux/amd64 '@@IMAGE@@'@'@@DIGEST@@'
else
  [[ -f "$OPS_ARTIFACT_PATH" && ! -L "$OPS_ARTIFACT_PATH" ]] || fail '缺少离线镜像，请上传 docker save 生成的 tar 或 gzip 文件（最大 2 GiB）'
  docker load --input "$OPS_ARTIFACT_PATH"
fi
[[ "$(docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' '@@IMAGE_ID@@')" == '@@IMAGE_ID@@ linux/amd64' ]] || fail '镜像版本或架构不符，请使用此版本的 Linux amd64 镜像'
[[ -z "$(docker ps -a --filter "name=^/$container$" --format '{{.ID}}')" ]] || fail '容器已存在，请核对原任务'
printf '%s\n' '官方镜像已核验'
