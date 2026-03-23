#!/bin/bash -xe

destination=$1
version=$(sed -n 's/^toolchain go//p' go.mod)
if [ -z "$version" ]; then
    version=$(sed -n 's/^go //p' go.mod)
fi
arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
tarball=go$version.linux-$arch.tar.gz
url=https://dl.google.com/go/

mkdir -p $destination
curl -L $url/$tarball -o $destination/$tarball
tar -xvf $destination/$tarball -C $destination
