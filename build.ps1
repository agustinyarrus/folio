# build.ps1 — compila Folio como .exe release (sin consola, con icono embebido).
# Uso:  .\build.ps1            -> genera folio.exe
#       .\build.ps1 -Debug     -> genera folio-debug.exe (con consola + logs FOLIO_DEBUG)

param([switch]$Debug)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$env:GOTOOLCHAIN = 'auto'

# Recurso de icono: regenerar folio.ico (si falta) y rsrc.syso desde el .ico.
if (-not (Test-Path folio.ico)) {
  Write-Host "Generando folio.ico..."
  & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'gen-icon.ps1')
}
if ((Test-Path folio.ico) -and -not (Test-Path rsrc.syso)) {
  Write-Host "Generando rsrc.syso desde folio.ico..."
  go run github.com/akavel/rsrc@latest -ico folio.ico -o rsrc.syso
}

if ($Debug) {
  go build -o folio-debug.exe .
  Write-Host "OK -> $(Resolve-Path folio-debug.exe)   (corre con FOLIO_DEBUG=1 para logs)"
} else {
  go build -ldflags="-H windowsgui -s -w" -o folio.exe .
  Write-Host "OK -> $(Resolve-Path folio.exe)"
}
