#!/usr/bin/env bash

set -xe

make container generate generate-deploy generate-test
if [[ -n "$(git status --porcelain)" ]] ; then
    echo "It seems like you need to run `make generate`. Please run it and commit the changes"
    git status --porcelain
    exit 1
fi
