$ErrorActionPreference = 'Stop'

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern IntPtr FindWindowEx(IntPtr parent, IntPtr child, string cls, string title);
}
"@

# Verifies close-to-tray: the process must survive WM_CLOSE and hide its window.
$WM_CLOSE = 0x0010

$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 10

if ($p.HasExited) { Write-Output "FAIL: process exited on startup"; exit 1 }

$proc = Get-Process -Id $p.Id
$hwnd = $proc.MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) { Write-Output "FAIL: main window not found"; Stop-Process -Id $p.Id -Force; exit 1 }

Write-Output ("visible before close : " + [Win32]::IsWindowVisible($hwnd))

# systray creates a hidden message window of class SystrayClass
$tray = [Win32]::FindWindowEx([IntPtr]::Zero, [IntPtr]::Zero, "SystrayClass", $null)
Write-Output ("systray window found : " + ($tray -ne [IntPtr]::Zero))

[void][Win32]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 3

$alive = -not $p.HasExited
Write-Output ("alive after close    : " + $alive)

if (-not $alive) {
  Write-Output "FAIL: process quit while closeToTray is enabled"
  exit 1
}

$visible = [Win32]::IsWindowVisible($hwnd)
Write-Output ("visible after close  : " + $visible)
Stop-Process -Id $p.Id -Force

if ($visible) {
  Write-Output "FAIL: window still visible"
  exit 1
}
Write-Output "PASS: close-to-tray works (process alive, window hidden)"
