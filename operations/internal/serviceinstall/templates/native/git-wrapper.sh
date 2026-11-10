
printf '#!/bin/sh\nGIT_EXEC_PATH=%s/runtime/git/usr/lib/git-core\nGIT_TEMPLATE_DIR=%s/runtime/git/usr/share/git-core/templates\nPERL5LIB=%s/runtime/git/usr/share/perl5\nexport GIT_EXEC_PATH GIT_TEMPLATE_DIR PERL5LIB\nexec %s/runtime/git/usr/bin/git "$@"\n' "$OPS_INSTALL_ROOT" "$OPS_INSTALL_ROOT" "$OPS_INSTALL_ROOT" "$OPS_INSTALL_ROOT" > "$OPS_BINARY"
chmod 755 "$OPS_BINARY"