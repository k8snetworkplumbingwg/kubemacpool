#!/bin/bash -e
#
# Copyright 2025 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

allowed_go_version=$(grep allowed_go go.mod | awk '{print $3}')
if [ -z "$allowed_go_version" ]; then
  echo "Error: go.mod Go allowed version not found" >&2
  exit 1
fi

current_go_version=$(awk '/^go [0-9]+\./ {print $2}' go.mod | awk -F. '{print $1"."$2}')
current_go_toolchain_version=$(grep '^toolchain' go.mod | awk '{print $2}' | sed 's/go//' | awk -F. '{print $1"."$2}' || echo "")

if [ "$current_go_version" != "$allowed_go_version" ]; then
    echo "Error: go.mod Go version $current_go_version different than allowed version allowed_go_version" >&2
    exit 1
fi

if [ -n "$current_go_toolchain_version" ]; then
    if [ "$current_go_toolchain_version" != "$allowed_go_version" ]; then
        echo "Error: Go toolchain version $current_go_toolchain_version different than allowed version allowed_go_version" >&2
        exit 1
    fi
fi
