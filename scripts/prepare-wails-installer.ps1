param()

$ErrorActionPreference = "Stop"

$projectRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$installerDir = Join-Path $projectRoot "build\windows\installer"
$projectFile = Join-Path $installerDir "project.nsi"

if (-not (Test-Path $projectFile)) {
    $gopath = (& go env GOPATH).Trim()
    $template = Get-ChildItem -Path (Join-Path $gopath "pkg\mod\github.com\wailsapp\wails") `
        -Directory -Filter "v2@*" |
        Sort-Object Name -Descending |
        ForEach-Object {
            Join-Path $_.FullName "pkg\buildassets\build\windows\installer\project.nsi"
        } |
        Where-Object { Test-Path $_ } |
        Select-Object -First 1

    if (-not $template) {
        throw "Could not find Wails NSIS project.nsi template in Go module cache."
    }

    New-Item -ItemType Directory -Force -Path $installerDir | Out-Null
    Copy-Item -LiteralPath $template -Destination $projectFile
}

$content = Get-Content -LiteralPath $projectFile -Raw
$content = $content -replace 'InstallDir "\$PROGRAMFILES64\\\$\{INFO_COMPANYNAME\}\\\$\{INFO_PRODUCTNAME\}"', 'InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}"'
Set-Content -LiteralPath $projectFile -Value $content -NoNewline
