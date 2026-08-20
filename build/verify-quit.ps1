$ErrorActionPreference = 'Stop'

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32Q {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
}
"@

# Verifies the opposite path: with closeToTray disabled and nothing streaming,
# WM_CLOSE must actually quit. Also checks portable-mode detection, since the
# presence of <exeDir>\data is what selects it.
$WM_CLOSE = 0x0010
$dataDir = Join-Path (Resolve-Path '.\build\bin') 'data'

New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
$cfg = '{"version":1,"settings":{"closeToTray":false,"preventSleep":true,"maxConcurrent":4,"probeIntervalSec":60,"theme":"dark"},"tools":[],"tasks":[]}'
Set-Content -Path (Join-Path $dataDir 'config.json') -Value $cfg -Encoding UTF8

$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 10

if ($p.HasExited) { Write-Output "FAIL: process exited on startup"; exit 1 }

$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) { Write-Output "FAIL: main window not found"; Stop-Process -Id $p.Id -Force; exit 1 }

# paths.Ensure should have created the portable layout next to the exe
foreach ($d in @('tools','logs','recordings','cache')) {
  $exists = Test-Path (Join-Path $dataDir $d)
  Write-Output ("portable dir " + $d.PadRight(11) + ": " + $exists)
  if (-not $exists) { Write-Output "FAIL: portable layout not created"; Stop-Process -Id $p.Id -Force; exit 1 }
}

[void][Win32Q]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 4

if ($p.HasExited) {
  Write-Output "PASS: quits on close when closeToTray is disabled"
  exit 0
}
Write-Output "FAIL: still running though closeToTray is disabled"
Stop-Process -Id $p.Id -Force
exit 1
