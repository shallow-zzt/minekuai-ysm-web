$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
Push-Location $projectRoot
try {
    Push-Location web
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw 'Web build failed' }
    } finally { Pop-Location }
    New-Item -ItemType Directory -Force dist | Out-Null
    Push-Location backend
    try {
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
        go build -trimpath -o ../dist/ysm-bridge.exe .
        if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    } finally { Pop-Location }
} finally { Pop-Location }
