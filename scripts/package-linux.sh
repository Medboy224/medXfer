#!/usr/bin/env bash
# Builds medXfer's Linux package (WP-B.6 of the J6b plan): the app's bundle in a tar.gz, with
# DEPENDANCES.txt, the system libraries it needs and, on Debian and Ubuntu, their packages
# (G2A-08: the package runs outside the build tree; the fresh distribution test is in the CI).
# Usage (from the repository root): bash scripts/package-linux.sh [out_dir]
# Prerequisites: those of scripts/build-lib.sh (Go, gcc), Flutter, the GTK 3 development files.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:-"$root/dist"}
version=$(sed -n 's/^version:[[:space:]]*\([^+[:space:]]*\).*/\1/p' "$root/app/pubspec.yaml")

# MEDXFER_SKIP_BUILD=1: the bundle is already built (the CI's app job).
if [ "${MEDXFER_SKIP_BUILD:-}" != 1 ]; then
  bash "$root/scripts/build-lib.sh"
  bash "$root/scripts/stage-app-native.sh"
  (cd "$root/app" && flutter build linux --release)
fi

bundle="$root/app/build/linux/x64/release/bundle"
for f in medxfer lib/libmedxfer.so lib/libflutter_linux_gtk.so data/icudtl.dat; do
  [ -e "$bundle/$f" ] || { echo "missing in the package: $f" >&2; exit 1; }
done

name="medxfer-$version-linux-x64"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp -a "$bundle" "$stage/$name"

# The system libraries: what the program and its libraries need, outside the bundle.
deps="$stage/$name/DEPENDANCES.txt"
{
  echo "medXfer $version pour Linux x64 : bibliothèques du système nécessaires."
  glibc=$(find "$stage/$name" -type f \( -name medxfer -o -name '*.so' \) -exec objdump -T {} + 2>/dev/null |
    grep -o 'GLIBC_[0-9.]*' | sort -uV | tail -1)
  echo "glibc ${glibc#GLIBC_} ou plus récente."
  echo
  echo "Bibliothèques :"
  find "$stage/$name" -type f \( -name medxfer -o -name '*.so' \) -exec ldd {} + 2>/dev/null |
    awk '/=>/ && $3 ~ /^\// {print $3}' | grep -v "/$name/" | sort -u > "$stage/libs"
  sed 's/^/  /' "$stage/libs"
  # OpenGL: libepoxy opens it while the app runs, so ldd does not see it. Without it the app
  # stops at once ("Couldn't open libEGL.so.1"), found on a fresh Ubuntu by the CI.
  echo "  libEGL.so.1, libGL.so.1, libGLESv2.so.2 et un pilote OpenGL : chargés au lancement"
  if command -v dpkg >/dev/null; then
    echo
    echo "Paquets Debian et Ubuntu (apt install) :"
    {
      # dpkg knows the real path: /lib is often a link to /usr/lib.
      while read -r lib; do
        dpkg -S "$(readlink -f "$lib")" 2>/dev/null || dpkg -S "$lib" 2>/dev/null || echo "no package owns $lib" >&2
      done < "$stage/libs" | cut -d: -f1
      # The OpenGL loaded at launch, and Mesa's drivers (software rendering without a GPU).
      printf '%s\n' libegl1 libgl1 libgles2 libgl1-mesa-dri
    } | sort -u | sed 's/^/  /'
  fi
} > "$deps"

mkdir -p "$out"
tar -C "$stage" -czf "$out/$name.tar.gz" "$name"
ls -l "$out/$name.tar.gz"
sha256sum "$out/$name.tar.gz"
cat "$deps"
