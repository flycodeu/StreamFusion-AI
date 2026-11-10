download() { need curl; curl --fail --location --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 1200 --retry 2 --retry-delay 2 --output "$2" "$1"; }
