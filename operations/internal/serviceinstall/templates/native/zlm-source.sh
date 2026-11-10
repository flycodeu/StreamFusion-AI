cmake -S . -B "$OPS_BUILD_ROOT/build" -DCMAKE_BUILD_TYPE=Release -DENABLE_TESTS=OFF -DENABLE_PYTHON=OFF -DENABLE_OPENSSL=ON -DENABLE_WEBRTC=ON -DENABLE_SRT=ON -DDISABLE_REPORT=ON
cmake --build "$OPS_BUILD_ROOT/build" --parallel "$OPS_BUILD_JOBS" --target MediaServer
install -m 755 -- release/linux/Release/MediaServer "$OPS_BINARY"