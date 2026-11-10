check_owner
create_service_user
java="$(cat "$root/config/java-binary")"
cat > "$root/bin/run" <<EOF
#!/bin/bash
export JENKINS_HOME='$root/data'
exec '$java' -Xms256m -Xmx$((OPS_MEMORY_MIB*3/4))m -jar '$root/runtime/jenkins.war' --httpListenAddress='$OPS_BIND_ADDRESS' --httpPort='$OPS_HOST_PORT' --webroot='$root/data/war'
EOF
write_unit
printf '%s\n' '首次打开 Jenkins 后按官方设置向导创建管理员；初始密码保存在本次数据目录 secrets/initialAdminPassword'
