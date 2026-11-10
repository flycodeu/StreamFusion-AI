./configure --disable-doc --disable-ffplay --disable-debug --enable-gpl --enable-version3 --enable-libx264 --enable-openssl
make -j"$OPS_BUILD_JOBS"
install -m 755 -- ffmpeg ffprobe "$OPS_INSTALL_ROOT/bin/"