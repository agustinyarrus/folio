<#
install.ps1 — instala Folio en Program Files, lo agrega al Menú de Inicio y lo asocia como
aplicación por defecto para Markdown. Se auto-eleva (UAC; mismo usuario -> tu HKCU se respeta).

  .\install.ps1            -> instala / actualiza
  .\install.ps1 -Uninstall -> desinstala

La asociación por defecto en Windows 10/11 está protegida con un hash anti-hijack en
HKCU\...\UserChoice; lo calcula SFTA.ps1 (Danysys, MIT) que vive al lado de este script.
#>
param([switch]$Uninstall)
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path

# ---- auto-elevación (UAC) ----
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
  $psExe = (Get-Process -Id $PID).Path
  $a = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$($MyInvocation.MyCommand.Path)`"")
  if ($Uninstall) { $a += '-Uninstall' }
  Write-Host "Pidiendo elevación (UAC)..."
  Start-Process -FilePath $psExe -Verb RunAs -ArgumentList $a -Wait
  return
}

# ---- constantes ----
$installDir = Join-Path $env:ProgramFiles 'Folio'
$exe        = Join-Path $installDir 'folio.exe'
$ico        = Join-Path $installDir 'folio.ico'       # icono de la app (documento.ico)
$fileIco    = Join-Path $installDir 'folio-file.ico'  # icono de los archivos .md (copia-md.ico)
$progId     = 'Folio.Document'
$exts       = '.md', '.markdown', '.mdown', '.mkd', '.mkdn', '.mdwn', '.mdtxt', '.mdtext', '.mdx', '.rmd', '.qmd'
$cls        = 'HKLM:\Software\Classes'
$startLnk   = Join-Path ([Environment]::GetFolderPath('CommonStartMenu')) 'Programs\Folio.lnk'

function Remove-Key($p) { if (Test-Path $p) { Remove-Item $p -Recurse -Force -ErrorAction SilentlyContinue } }

# ============================ DESINSTALAR ============================
if ($Uninstall) {
  foreach ($e in $exts) {
    Remove-Key "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\$e\UserChoice"
    Remove-ItemProperty "$cls\$e\OpenWithProgids" -Name $progId -ErrorAction SilentlyContinue
    $d = (Get-ItemProperty "$cls\$e" -Name '(default)' -ErrorAction SilentlyContinue).'(default)'
    if ($d -eq $progId) { Remove-ItemProperty "$cls\$e" -Name '(default)' -ErrorAction SilentlyContinue }
  }
  Remove-Key "$cls\$progId"
  Remove-Key 'HKLM:\Software\Folio'
  Remove-ItemProperty 'HKLM:\Software\RegisteredApplications' -Name 'Folio' -ErrorAction SilentlyContinue
  Remove-Key 'HKLM:\Software\Microsoft\Windows\CurrentVersion\App Paths\folio.exe'
  Remove-Key $startLnk
  Remove-Key $installDir
  Write-Host "Folio desinstalado. (.md queda sin app por defecto: Windows preguntará la próxima vez)"
  return
}

# ============================ INSTALAR ============================
if (-not (Test-Path (Join-Path $here 'folio.exe'))) { throw "No existe folio.exe en $here; corré build.ps1 primero." }

# 1) copiar a Program Files (cerrar instancia previa instalada si está corriendo)
Get-Process folio -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } | Stop-Process -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $installDir | Out-Null
Copy-Item (Join-Path $here 'folio.exe') $exe -Force
if (Test-Path (Join-Path $here 'folio.ico')) { Copy-Item (Join-Path $here 'folio.ico') $ico -Force }
if (Test-Path (Join-Path $here 'folio-file.ico')) { Copy-Item (Join-Path $here 'folio-file.ico') $fileIco -Force }

# 2) ProgId machine-wide
New-Item -Path "$cls\$progId\shell\open\command" -Force | Out-Null
Set-ItemProperty "$cls\$progId" '(default)' 'Documento Markdown'
Set-ItemProperty "$cls\$progId" 'FriendlyTypeName' 'Documento Markdown'
New-Item -Path "$cls\$progId\DefaultIcon" -Force | Out-Null
# icono de los ARCHIVOS .md = folio-file.ico (copia-md.ico); fallback al de la app si faltara
$docIcon = if (Test-Path $fileIco) { "$fileIco,0" } else { "$ico,0" }
Set-ItemProperty "$cls\$progId\DefaultIcon" '(default)' $docIcon
# OJO: NO re-crear "shell\open" con -Force acá: en el registro -Force borra la clave existente
# y se llevaría puesto el subkey "command" recién creado arriba. Solo seteamos su propiedad.
Set-ItemProperty "$cls\$progId\shell\open" 'FriendlyAppName' 'Folio'
Set-ItemProperty "$cls\$progId\shell\open\command" '(default)' "`"$exe`" `"%1`""

# 3) App Paths + Capabilities + RegisteredApplications (aparece en Configuración > Apps predeterminadas)
$ap = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\App Paths\folio.exe'
New-Item $ap -Force | Out-Null
Set-ItemProperty $ap '(default)' $exe
Set-ItemProperty $ap 'Path' $installDir
$cap = 'HKLM:\Software\Folio\Capabilities'
New-Item "$cap\FileAssociations" -Force | Out-Null
Set-ItemProperty 'HKLM:\Software\Folio\Capabilities' 'ApplicationName' 'Folio'
Set-ItemProperty 'HKLM:\Software\Folio\Capabilities' 'ApplicationDescription' 'Lector de Markdown ultraminimalista'
if (Test-Path $ico) { Set-ItemProperty 'HKLM:\Software\Folio\Capabilities' 'ApplicationIcon' "$ico,0" }
foreach ($e in $exts) { Set-ItemProperty "$cap\FileAssociations" $e $progId }
if (-not (Test-Path 'HKLM:\Software\RegisteredApplications')) { New-Item 'HKLM:\Software\RegisteredApplications' -Force | Out-Null }
Set-ItemProperty 'HKLM:\Software\RegisteredApplications' 'Folio' 'Software\Folio\Capabilities'

# 4) OpenWithProgids (aparece en "Abrir con") + default legacy si la ext no tiene
foreach ($e in $exts) {
  if (-not (Test-Path "$cls\$e\OpenWithProgids")) { New-Item -Path "$cls\$e\OpenWithProgids" -Force | Out-Null }
  New-ItemProperty "$cls\$e\OpenWithProgids" -Name $progId -Value ([byte[]]@()) -PropertyType None -Force | Out-Null
  $d = (Get-ItemProperty "$cls\$e" -Name '(default)' -ErrorAction SilentlyContinue).'(default)'
  if ([string]::IsNullOrEmpty($d)) { Set-ItemProperty "$cls\$e" '(default)' $progId }
}

# 5) acceso directo en el Menú de Inicio (todos los usuarios)
$ws = New-Object -ComObject WScript.Shell
$sc = $ws.CreateShortcut($startLnk)
$sc.TargetPath       = $exe
$sc.WorkingDirectory = $installDir
if (Test-Path $ico) { $sc.IconLocation = "$ico,0" }
$sc.Description = 'Folio — lector de Markdown'
$sc.Save()

# 6) default real (UserChoice con hash) vía SFTA
$sfta = Join-Path $here 'SFTA.ps1'
$okExts = @()
if (Test-Path $sfta) {
  . $sfta
  foreach ($e in $exts) {
    try {
      Set-FTA -ProgId $progId -Extension $e | Out-Null
      $cur = (Get-ItemProperty "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\$e\UserChoice" -ErrorAction SilentlyContinue).ProgId
      if ($cur -eq $progId) { $okExts += $e }
    } catch { Write-Host "  (no se pudo fijar $e por defecto)" }
  }
} else {
  Write-Host "  (SFTA.ps1 no encontrado: no se fija el default automáticamente)"
}

Write-Host ""
Write-Host "==================== Folio instalado ===================="
Write-Host "  Exe:           $exe"
Write-Host "  Menú de Inicio: $startLnk"
if ($okExts.Count) { Write-Host "  Por defecto en: $($okExts -join ' ')" }
else { Write-Host "  Default: no se pudo fijar (usá Configuración > Apps predeterminadas > .md > Folio)" }
Write-Host "========================================================="
