$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
if (-not (Test-Path config.json)) {
    throw 'Copy config.example.json to config.json and fill in the required settings first.'
}
if (-not (Test-Path ./dist/ysm-bridge.exe)) {
    throw 'dist/ysm-bridge.exe is missing. Extract the complete project package first.'
}
& ./dist/ysm-bridge.exe -config config.json
if ($LASTEXITCODE -ne 0) {
    throw "YSM Bridge stopped with exit code $LASTEXITCODE. See data/logs/startup.log."
}
