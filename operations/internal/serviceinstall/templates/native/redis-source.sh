make -j"$OPS_BUILD_JOBS" MALLOC=libc BUILD_TLS=yes redis-server redis-cli
install -m 755 -- src/redis-server src/redis-cli "$OPS_INSTALL_ROOT/bin/"