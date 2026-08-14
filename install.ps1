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
# Markdown: Folio se queda como visor PREDETERMINADO de estas (paso 6, vía SFTA).
$mdExts     = '.md', '.markdown', '.mdown', '.mkd', '.mkdn', '.mdwn', '.mdtxt', '.mdtext', '.mdx', '.rmd', '.qmd'
# El resto de los formatos que Folio sabe abrir: SOLO se agregan a "Abrir con".
# Nunca se fijan por defecto — sería una grosería robarle los .json al editor o
# los .docx a Word.
$extraExts  = '.json', '.jsonc', '.json5', '.jsonl', '.ndjson', '.csv', '.tsv', '.tab',
              '.yaml', '.yml', '.toml', '.ini', '.cfg', '.conf', '.properties', '.env',
              '.xml', '.plist', '.rss', '.atom', '.diff', '.patch', '.reg',
              '.txt', '.log', '.nfo', '.rst', '.rest', '.adoc', '.asciidoc', '.asc',
              '.org', '.wiki', '.mediawiki', '.html', '.htm', '.xhtml',
              '.ipynb', '.docx', '.odt', '.epub',
              '.go', '.py', '.js', '.ts', '.tsx', '.jsx', '.c', '.h', '.cpp', '.hpp',
              '.cs', '.java', '.kt', '.swift', '.rs', '.rb', '.php', '.lua', '.r',
              '.sh', '.bash', '.ps1', '.psm1', '.bat', '.cmd', '.sql', '.css', '.scss',
              '.vue', '.svelte', '.tf', '.proto', '.graphql', '.gradle', '.dart'
$exts       = $mdExts + $extraExts
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

# El subsistema del PE tiene que ser GUI (2). Un "go build" a secas deja el exe
# como consola (3) y entonces Folio abre una ventana negra de cmd al lado, que
# encima solo se cierra con la app. Mejor cortar acá que instalar eso.
function Get-PESubsystem($path) {
  $fs = [System.IO.File]::OpenRead($path)
  try {
    $br = New-Object System.IO.BinaryReader($fs)
    $fs.Position = 0x3C
    $peOff = $br.ReadInt32()
    $fs.Position = $peOff + 4 + 20 + 68   # firma + COFF + offset del Subsystem (PE32+)
    return $br.ReadUInt16()
  } finally { $fs.Close() }
}
$sub = Get-PESubsystem (Join-Path $here 'folio.exe')
if ($sub -ne 2) {
  throw "folio.exe está compilado como aplicación de consola (subsystem $sub): abriría una ventana negra al lado. Recompilá con .\build.ps1 (usa -H windowsgui)."
}

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
  # el default legacy solo para Markdown, y solo si la extensión no tiene dueño
  if ($mdExts -contains $e) {
    $d = (Get-ItemProperty "$cls\$e" -Name '(default)' -ErrorAction SilentlyContinue).'(default)'
    if ([string]::IsNullOrEmpty($d)) { Set-ItemProperty "$cls\$e" '(default)' $progId }
  }
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
  foreach ($e in $mdExts) {   # SOLO Markdown: el resto queda en "Abrir con"
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
