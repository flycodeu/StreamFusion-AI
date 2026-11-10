
printf '#!/bin/sh\nJAVA_HOME=%s/runtime/jdk\nexport JAVA_HOME\nexec %s/runtime/maven/bin/mvn "$@"\n' "$OPS_INSTALL_ROOT" "$OPS_INSTALL_ROOT" > "$OPS_BINARY"
chmod 755 "$OPS_BINARY"