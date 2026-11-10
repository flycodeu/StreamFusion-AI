check_owner
need docker
docker info --format '{{.OSType}}' | grep -qx linux || fail 'Docker 服务不可用'
if [[ "$OPS_SOURCE" == REGISTRY ]]; then
  image='docker.io/heartexlabs/label-studio:@@VERSION@@'
  docker pull --platform linux/amd64 "$image@"'@@IMAGE_DIGEST@@'
else
  [[ -f "$OPS_ARTIFACT_PATH" && ! -L "$OPS_ARTIFACT_PATH" ]] || fail '请上传对应版本的 docker save 离线镜像'
  docker load --input "$OPS_ARTIFACT_PATH"
fi
[[ "$(docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' '@@IMAGE_ID@@')" == '@@IMAGE_ID@@ linux/amd64' ]] || fail '镜像版本或架构不符'
[[ -z "$(docker ps -a --filter "name=^/$container$" --format '{{.ID}}')" ]] || fail '容器已存在，请核对原任务'
