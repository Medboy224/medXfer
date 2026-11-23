#!/usr/bin/env bash
# The Linux package on a fresh distribution (G2A-08, reserve 2 of G2a; WP-B.6 of the J6b plan):
# in a fresh Ubuntu container (24.04 by default) where nothing else is installed, the packages
# DEPENDANCES.txt lists are installed, every library is found, and the app runs for 15 seconds
# (on Xvfb) without stopping.
# Usage: bash scripts/test-fresh-linux.sh dist/medxfer-<version>-linux-x64.tar.gz [image]
# APT_MIRROR (optional, e.g. http://azure.archive.ubuntu.com): the same Ubuntu packages from that
# mirror rather than archive.ubuntu.com. On GitHub's machines, the public servers made the install
# last 22 to 32 minutes on 23 November 2026, against seconds from the mirror they use themselves.
set -euo pipefail
archive=$(readlink -f "$1")
image=${2:-ubuntu:24.04}
name=$(basename "$archive" .tar.gz)

docker run --rm -e APT_MIRROR="${APT_MIRROR:-}" -v "$archive:/pkg.tar.gz:ro" "$image" bash -euo pipefail -c "
  export DEBIAN_FRONTEND=noninteractive
  if [ -n \"\$APT_MIRROR\" ]; then
    # 22.04: sources.list; 24.04: sources.list.d/ubuntu.sources.
    sed -i -E \"s#https?://(archive|security)\.ubuntu\.com#\$APT_MIRROR#g\" /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null || true
    echo \"Packages from \$APT_MIRROR\"
  fi
  mkdir -p /opt && tar -C /opt -xzf /pkg.tar.gz
  cd /opt/$name
  # The packages of DEPENDANCES.txt: the lines after their heading, up to the blank line.
  pkgs=\$(sed -n '/^Paquets/,/^\$/{/^  /p}' DEPENDANCES.txt | tr -d ' ')
  echo \"Packages: \$pkgs\"
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \$pkgs xvfb xauth >/dev/null
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
