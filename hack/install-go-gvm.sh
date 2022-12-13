#!/bin/bash -e

install_gvm() {
	echo "Installing gvm"
  bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
  source  ~/.gvm/scripts/gvm
}

install_go() {
	local go_version=$1
	echo "Installing ${go_version} with gvm"
  gvm install "${go_version}"
  gvm use "${go_version}" --default
}

if ! gvm version > /dev/null 2>&1; then
	install_gvm
fi

GO_VERSION=go"$(hack/go-version.sh)"
if ! go version | grep "${GO_VERSION}"; then
	install_go "${GO_VERSION}"
fi
