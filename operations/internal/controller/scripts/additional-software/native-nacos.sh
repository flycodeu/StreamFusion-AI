check_owner
create_service_user
[[ "$OPS_PORT_GRPC" -eq $((OPS_HOST_PORT+1000)) && "$OPS_PORT_CLUSTER_GRPC" -eq $((OPS_HOST_PORT+1001)) ]] || fail 'Nacos gRPC 端口须分别为主端口 +1000、+1001'
java="$(cat "$root/config/java-binary")"
token="$(read_secret "$OPS_SECRET_AUTH_TOKEN_FILE")"; identity="$(read_secret "$OPS_SECRET_AUTH_IDENTITY_FILE")"
[[ "$token" =~ ^[A-Za-z0-9+/]+=*$ && $(printf '%s' "$token" | base64 --decode | wc -c) -ge 32 ]] || fail 'Token 签名密钥需要 Base64 编码，解码后至少 32 字节'
[[ "$identity" =~ ^[A-Za-z0-9_.-]{8,128}$ ]] || fail '服务身份密钥请使用 8 至 128 位字母、数字、点、横线或下划线'
cp "$root/runtime/conf/nacos-logback.xml" "$root/config/nacos-logback.xml"
cat > "$root/config/application.properties" <<EOF
nacos.server.main.port=$OPS_HOST_PORT
nacos.console.port=$OPS_PORT_CONSOLE
server.address=$OPS_BIND_ADDRESS
nacos.server.contextPath=/nacos
nacos.console.contextPath=
nacos.plugin.auth.type=nacos
nacos.core.auth.enabled=true
nacos.core.auth.admin.enabled=true
nacos.core.auth.console.enabled=true
nacos.core.auth.server.identity.key=ops-server
nacos.core.auth.server.identity.value=$identity
nacos.plugin.auth.nacos.token.secret.key=$token
nacos.ai.mcp.registry.enabled=false
nacos.ai.skill.registry.enabled=false
EOF
if [[ "$OPS_BIND_ADDRESS" != 0.0.0.0 ]]; then
  printf 'nacos.inetutils.ip-address=%s\n' "$OPS_BIND_ADDRESS" >> "$root/config/application.properties"
fi
chown root:"$(id -gn "$service_user")" "$root/config/"*.xml "$root/config/application.properties"
chmod 640 "$root/config/"*.xml "$root/config/application.properties"
rm -rf -- "$root/runtime/data" "$root/runtime/logs"
ln -s "$root/data" "$root/runtime/data"
ln -s "$root/logs" "$root/runtime/logs"
cat > "$root/bin/run" <<EOF
#!/bin/bash
exec '$java' -Xms256m -Xmx$((OPS_MEMORY_MIB*3/4))m --add-opens=java.base/java.lang=ALL-UNNAMED --add-opens=java.base/java.lang.reflect=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED -Dnacos.standalone=true -Dnacos.remote.grpc.listen.ip='$OPS_BIND_ADDRESS' -Dnacos.deployment.type=merged -Dnacos.home='$root/runtime' -Dloader.path='$root/runtime/plugins,$root/runtime/plugins/health,$root/runtime/plugins/cmdb,$root/runtime/plugins/selector' -jar '$root/runtime/target/nacos-server.jar' --spring.config.additional-location='file:$root/config/' --logging.config='$root/config/nacos-logback.xml' --server.max-http-request-header-size=524288 nacos.nacos
EOF
write_unit
unset token identity
