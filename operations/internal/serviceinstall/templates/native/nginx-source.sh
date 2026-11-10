./configure --prefix="$OPS_INSTALL_ROOT" --conf-path="$OPS_INSTALL_ROOT/config/nginx.conf" --pid-path="$OPS_INSTALL_ROOT/data/nginx.pid" --error-log-path=stderr --http-log-path=/dev/stdout --with-http_ssl_module --with-http_v2_module
make -j"$OPS_BUILD_JOBS"
install -m 755 -- objs/nginx "$OPS_BINARY"