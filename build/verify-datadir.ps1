$ErrorActionPreference = 'Stop'

# Verifies LIVERELAY_DATA: with it set, the app must use that directory as its
# data root and must NOT fall back to %APPDATA%\LiveRelay (installed mode).
$appData = Join-Path $env:APPDATA 'LiveRelay'
$dataDir = Resolve-Path '.\data'

if (Test-Path $appData) {
  Remove-Item -Recurse -Force $appData
  Write-Output "removed stale $appData"
}

$env:LIVERELAY_DATA = $dataDir
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 10
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output "FAIL: process exited on startup"; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
Write-Output ("window opened       : " + ($hwnd -ne [IntPtr]::Zero))

$fellBack = Test-Path $appData
Write-Output ("fell back to APPDATA: " + $fellBack)

# The configured kernels must be reachable from the data root the app is using
foreach ($rel in @('tools\yt-dlp.exe', 'tools\ffmpeg.exe', 'tools\streamlink\bin\streamlink.exe')) {
  $ok = Test-Path (Join-Path $dataDir $rel)
  Write-Output ("kernel " + $rel.PadRight(38) + ": " + $ok)
  if (-not $ok) { Stop-Process -Id $p.Id -Force; Write-Output "FAIL: kernel missing"; exit 1 }
}

Stop-Process -Id $p.Id -Force
Start-Sleep -Seconds 1

if ($fellBack) {
  Write-Output "FAIL: LIVERELAY_DATA ignored, app created an APPDATA data root"
  exit 1
}
Write-Output "PASS: app uses LIVERELAY_DATA and finds the bundled kernels"
