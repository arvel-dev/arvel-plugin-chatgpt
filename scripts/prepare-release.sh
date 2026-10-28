#!/bin/sh
# SPDX-License-Identifier: MIT-0
# GoReleaser runs this hook before it archives any platform.
set -eu
if [ "$#" -ne 1 ]
then
    echo 'usage: sh scripts/prepare-release.sh VERSION' >&2
    exit 2
fi
mkdir -p .release
jq --arg version "$1" '.version = $version' arvel.json > .release/arvel.json.tmp
mv .release/arvel.json.tmp .release/arvel.json
