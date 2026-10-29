#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 Andrea Casali
#
# Builds xdebug.so for one PHP version, for the static-php-cli "bulk" PHP-FPM
# build Covo pins for that version (NTS, non-debug), and checks it against
# that exact binary.
#
#   build.sh <php version> <php source sha256> <bulk fpm sha256> <out dir>
#
# Needs: a C compiler, autoconf (for phpize), curl, shasum.
# XDEBUG_VERSION and XDEBUG_SHA256 come from the environment.
set -euo pipefail

php_version=$1 php_sha256=$2 bulk_sha256=$3 out=$4
: "${XDEBUG_VERSION:?}" "${XDEBUG_SHA256:?}"
export MACOSX_DEPLOYMENT_TARGET=12.0 # same as the bulk builds

arch=$(uname -m)
[ "$arch" = arm64 ] || { echo "arm64 only" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"

fetch() { # url sha256 file
	curl -fsSL --retry 3 -o "$3" "$1"
	echo "$2  $3" | shasum -a 256 -c -
}

cd "$work"
fetch "https://www.php.net/distributions/php-$php_version.tar.xz" "$php_sha256" php.tar.xz
fetch "https://xdebug.org/files/xdebug-$XDEBUG_VERSION.tgz" "$XDEBUG_SHA256" xdebug.tgz
fetch "https://dl.static-php.dev/static-php-cli/bulk/php-$php_version-fpm-macos-aarch64.tar.gz" "$bulk_sha256" bulk.tar.gz

# A minimal PHP of the same version provides phpize, php-config and headers.
# Only the Zend engine ABI matters: same version, NTS, non-debug.
tar -xJf php.tar.xz
(
	cd "php-$php_version"
	./configure --prefix="$work/php" --disable-all --disable-cgi --disable-phpdbg \
		--disable-debug --disable-zts --without-pear >/dev/null
	make -j"$(sysctl -n hw.ncpu)" >/dev/null
	make install >/dev/null
)

tar -xzf xdebug.tgz
(
	cd "xdebug-$XDEBUG_VERSION"
	"$work/php/bin/phpize" >/dev/null
	./configure --with-php-config="$work/php/bin/php-config" >/dev/null
	make -j"$(sysctl -n hw.ncpu)" >/dev/null
)
so="xdebug-$XDEBUG_VERSION/modules/xdebug.so"

# Check it against the bulk binary Covo runs.
tar -xzf bulk.tar.gz php-fpm
fpm=$work/php-fpm
version=$("$fpm" -n -v)
case $version in "PHP $php_version "*) ;; *) echo "bulk binary is not PHP $php_version" >&2; exit 1 ;; esac

# With chained fixups (macOS 12+ targets) every symbol is bound when the
# extension is loaded, so loading it below proves php-fpm exports all that
# xdebug.so needs from PHP.
case $(otool -l "$so") in *LC_DYLD_CHAINED_FIXUPS*) ;; *) echo "xdebug.so does not use chained fixups" >&2; exit 1 ;; esac
# For the log: what is resolved outside php-fpm (system libraries).
nm -gU "$fpm" | awk '{print $3}' | sort -u >exported.txt
nm -u "$so" | sort -u >needed.txt
echo "symbols resolved outside php-fpm: $(comm -23 needed.txt exported.txt | tr '\n' ' ')"

modules=$("$fpm" -n -d "zend_extension=$work/$so" -m)
case $'\n'$modules$'\n' in *$'\n'Xdebug$'\n'*) ;; *) echo "php-fpm did not load xdebug.so: $modules" >&2; exit 1 ;; esac
version=$("$fpm" -n -d "zend_extension=$work/$so" -v)
case $version in *"with Xdebug v$XDEBUG_VERSION,"*) ;; *) echo "unexpected version: $version" >&2; exit 1 ;; esac

name="xdebug-$XDEBUG_VERSION-php-$php_version-macos-arm64.so"
cp "$so" "$out/$name"
(cd "$out" && shasum -a 256 "$name" >"$name.sha256")
cat "$out/$name.sha256"
