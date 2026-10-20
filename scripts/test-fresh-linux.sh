#!/usr/bin/env bash
# The Linux package on a fresh distribution (G2A-08, reserve 2 of G2a; WP-B.6 of the J6b plan):
# in an Ubuntu 24.04 container where nothing else is installed, the packages DEPENDANCES.txt
# lists are installed, every library is found, and the app runs for 15 seconds (on Xvfb)
# without stopping.
# Usage: bash scripts/test-fresh-linux.sh dist/medxfer-<version>-linux-x64.tar.gz [image]
set -euo pipefail
archive=$(readlink -f "$1")
image=${2:-ubuntu:24.04}
name=$(basename "$archive" .tar.gz)

docker run --rm -v "$archive:/pkg.tar.gz:ro" "$image" bash -euo pipefail -c "
  export DEBIAN_FRONTEND=noninteractive
  mkdir -p /opt && tar -C /opt -xzf /pkg.tar.gz
  cd /opt/$name
  # The packages of DEPENDANCES.txt: the lines after their heading, up to the blank line.
  pkgs=\$(sed -n '/^Paquets/,/^\$/{/^  /p}' DEPENDANCES.txt | tr -d ' ')
  echo \"Packages: \$pkgs\"
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \$pkgs libgl1-mesa-dri xvfb xauth >/dev/null
  # The plugins find libflutter_linux_gtk.so in lib/, as the app loads them.
  missing=\$(LD_LIBRARY_PATH=lib ldd medxfer lib/*.so | grep 'not found' || true)
  if [ -n \"\$missing\" ]; then echo \"Missing libraries:\"; echo \"\$missing\"; exit 1; fi
  echo 'Every library is found.'
  set +e
  xvfb-run -a timeout 15 ./medxfer > /tmp/app.log 2>&1
  code=\$?
  set -e
  tail -20 /tmp/app.log
  # 124: stopped by timeout, so it was still running after 15 s.
  if [ \$code -ne 124 ]; then echo \"The app stopped by itself (code \$code).\"; exit 1; fi
  echo 'The app ran for 15 s on a fresh $image.'
"
