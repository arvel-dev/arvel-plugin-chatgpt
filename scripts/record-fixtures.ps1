# SPDX-License-Identifier: Apache-2.0
$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    & go run ./cmd/record-fixtures @args
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
