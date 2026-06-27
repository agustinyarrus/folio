# gen-icon.ps1 — genera el glifo de pagina doblada (icono ORIGINAL de Folio).
# PowerShell 5.1 / System.Drawing. Produce un .ico multi-tamano con frames PNG (Vista+).
#   .\gen-icon.ps1
#
# OJO: el repo ya trae iconos curados -> folio.ico (icono de la app) y folio-file.ico (icono de
#      los archivos .md asociados). Este script SOBREESCRIBE folio.ico con el diseño generado;
#      corrélo solo si querés volver al icono original.
#
# Nota PS: el operador coma liga MAS fuerte que '*', asi que toda expresion 'n*$k' dentro de una
# lista de argumentos separada por comas va entre parentesis (si no, multiplica arrays y revienta).

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
Add-Type -AssemblyName System.Drawing

function New-RoundedPath([single]$x, [single]$y, [single]$w, [single]$h, [single]$r) {
  $p = New-Object System.Drawing.Drawing2D.GraphicsPath
  $d = $r * 2
  $p.AddArc($x, $y, $d, $d, 180, 90)
  $p.AddArc(($x + $w - $d), $y, $d, $d, 270, 90)
  $p.AddArc(($x + $w - $d), ($y + $h - $d), $d, $d, 0, 90)
  $p.AddArc($x, ($y + $h - $d), $d, $d, 90, 90)
  $p.CloseFigure()
  return $p
}

function PF([single]$x, [single]$y) { New-Object System.Drawing.PointF($x, $y) }

function New-Frame([int]$s) {
  $bmp = New-Object System.Drawing.Bitmap($s, $s, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.SmoothingMode     = 'AntiAlias'
  $g.InterpolationMode = 'HighQualityBicubic'
  $g.PixelOffsetMode   = 'HighQuality'
  $k = $s / 256.0

  # ---- fondo squircle con degrade azul-noche ----
  $bgPath = New-RoundedPath (8*$k) (8*$k) (240*$k) (240*$k) (58*$k)
  $rect = New-Object System.Drawing.RectangleF(([single](8*$k)), ([single](8*$k)), ([single](240*$k)), ([single](240*$k)))
  $c1 = [System.Drawing.Color]::FromArgb(255, 18, 22, 36)
  $c2 = [System.Drawing.Color]::FromArgb(255, 8, 9, 13)
  $bgBrush = New-Object System.Drawing.Drawing2D.LinearGradientBrush($rect, $c1, $c2, [single]90)
  $g.FillPath($bgBrush, $bgPath)
  $bd = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(40, 122, 162, 247), [single](2*$k))
  $g.DrawPath($bd, $bgPath)

  # ---- glifo de pagina ----
  $accent = [System.Drawing.Color]::FromArgb(255, 122, 162, 247)
  $paper  = [System.Drawing.Color]::FromArgb(255, 20, 25, 38)
  $foldBg = [System.Drawing.Color]::FromArgb(255, 30, 38, 60)
  $faint  = [System.Drawing.Color]::FromArgb(255, 86, 95, 137)

  $page = New-Object System.Drawing.Drawing2D.GraphicsPath
  $page.AddPolygon( @( (PF (80*$k) (56*$k)), (PF (156*$k) (56*$k)), (PF (184*$k) (84*$k)), (PF (184*$k) (204*$k)), (PF (80*$k) (204*$k)) ) )
  $page.CloseFigure()
  $g.FillPath((New-Object System.Drawing.SolidBrush($paper)), $page)
  $pPen = New-Object System.Drawing.Pen($accent, [single](7*$k))
  $pPen.LineJoin = 'Round'
  $g.DrawPath($pPen, $page)

  $fold = New-Object System.Drawing.Drawing2D.GraphicsPath
  $fold.AddPolygon( @( (PF (156*$k) (56*$k)), (PF (156*$k) (84*$k)), (PF (184*$k) (84*$k)) ) )
  $fold.CloseFigure()
  $g.FillPath((New-Object System.Drawing.SolidBrush($foldBg)), $fold)
  $g.DrawPath($pPen, $fold)

  $lp = New-Object System.Drawing.Pen($faint, [single](7*$k))
  $lp.StartCap = 'Round'; $lp.EndCap = 'Round'
  $g.DrawLine($lp, [single](100*$k), [single](116*$k), [single](164*$k), [single](116*$k))
  $g.DrawLine($lp, [single](100*$k), [single](142*$k), [single](164*$k), [single](142*$k))
  $ap = New-Object System.Drawing.Pen($accent, [single](7*$k))
  $ap.StartCap = 'Round'; $ap.EndCap = 'Round'
  $g.DrawLine($ap, [single](100*$k), [single](168*$k), [single](140*$k), [single](168*$k))

  $g.Dispose()
  $ms = New-Object System.IO.MemoryStream
  $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  return ,$ms.ToArray()   # coma: preserva el Byte[] (sin ella PS lo desenrolla a Object[])
}

$sizes = 16, 24, 32, 48, 64, 128, 256
$frames = @()
foreach ($s in $sizes) { $frames += , (New-Frame $s) }

$out = New-Object System.IO.MemoryStream
$bw = New-Object System.IO.BinaryWriter($out)
$bw.Write([UInt16]0); $bw.Write([UInt16]1); $bw.Write([UInt16]$sizes.Count)
$offset = 6 + 16 * $sizes.Count
for ($i = 0; $i -lt $sizes.Count; $i++) {
  $s = $sizes[$i]; $f = $frames[$i]
  if ($s -ge 256) { $dim = 0 } else { $dim = $s }
  $bw.Write([byte]$dim); $bw.Write([byte]$dim)
  $bw.Write([byte]0); $bw.Write([byte]0)
  $bw.Write([UInt16]1); $bw.Write([UInt16]32)
  $bw.Write([UInt32]$f.Length); $bw.Write([UInt32]$offset)
  $offset += $f.Length
}
foreach ($f in $frames) { $bw.Write($f) }
$bw.Flush()
[System.IO.File]::WriteAllBytes((Join-Path $PSScriptRoot 'folio.ico'), $out.ToArray())
$bw.Dispose()
Write-Host ("folio.ico generado ({0:N0} bytes, {1} tamanos)" -f (Get-Item folio.ico).Length, $sizes.Count)
