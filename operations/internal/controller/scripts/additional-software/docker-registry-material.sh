need docker
need curl
[[ "$OPS_MEMORY_MIB" -ge @@MEMORY@@ ]] || fail '此软件至少需要 @@MEMORY@@ MiB 安装内存'
docker info --format '{{.OSType}}' | grep -qx linux || fail 'Docker 服务不可用'

ref='@@REGISTRY_REF@@'
cached=true
if ! details="$(docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' "$ref" 2>/dev/null)"; then
  cached=false
  docker pull --platform linux/amd64 "$ref" || fail '固定版本镜像拉取失败，请检查 Docker 镜像仓库连接后重试'
  details="$(docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' "$ref")" || fail '镜像拉取完成，但 Docker 无法按固定仓库引用读取，请查看 Docker 服务日志'
fi
read -r image_id platform <<<"$details"
[[ "$platform" == linux/amd64 ]] || fail '镜像平台不符，此安装方案需要 Linux amd64 镜像'
# Classic and containerd stores expose different IDs for this fixed platform manifest.
[[ "$image_id" == '@@IMAGE_ID@@' || "$image_id" == '@@DIGEST@@' ]] || fail '镜像摘要与安装方案的固定版本不符，请核对镜像来源'
[[ -z "$(docker ps -a --filter "name=^/$container$" --format '{{.ID}}')" ]] || fail '容器已存在，请核对原任务'
if [[ "$cached" == true ]]; then
  printf '%s\n' '固定版本镜像已核验，复用本地镜像'
else
  printf '%s\n' '官方镜像已核验'
fi
