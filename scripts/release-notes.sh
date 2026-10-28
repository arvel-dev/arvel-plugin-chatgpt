#!/bin/sh
# SPDX-License-Identifier: MIT-0
# Run from the directory containing CHANGELOG.md. VERSION has no leading v.
set -eu
if [ "$#" -ne 1 ]
then
    echo 'usage: sh scripts/release-notes.sh VERSION' >&2
    exit 2
fi
awk -v version="$1" '
BEGIN {
    if (version !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)*$/) {
        print "release-notes: invalid version (omit the v prefix)" > "/dev/stderr"
        invalid = 1
        exit 2
    }
    heading = "## [" version "]"
}
/^## / {
    if (active) { active = 0; finished = 1 }
    if (!finished && ($0 == heading || index($0, heading " - ") == 1)) {
        found = 1
        active = 1
        next
    }
}
active {
    lines[++count] = $0
    if ($0 ~ /[^[:space:]]/ && $0 !~ /^[[:space:]]*#/) content = 1
}
END {
    if (invalid) exit 2
    if (!found || !content) {
        print "release-notes: version section is missing or empty: " version > "/dev/stderr"
        exit 1
    }
    for (i = 1; i <= count; i++) print lines[i]
}' CHANGELOG.md
