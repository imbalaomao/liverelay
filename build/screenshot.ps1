$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()

Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Cap {
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int cx, int cy, uint flags);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
}
"@

# WebView2 的内容是 GPU 合成的，PrintWindow 抓不到（只会得到一张纯背景色）。
# 改成把窗口置顶再抓屏：不抢焦点，只是短暂盖在别的窗口上面，截完还原层级。
# 需要用 pwsh 7 运行：Windows PowerShell 5.1 会把这些中文注释按 ANSI 读坏，
# 连带吞掉后面的语句。
$HWND_TOPMOST = [IntPtr]-1
$HWND_NOTOPMOST = [IntPtr]-2
$SWP = 0x0001 -bor 0x0002 -bor 0x0010  # NOMOVE | NOSIZE | NOACTIVATE

$name = if ($args.Count -gt 0) { $args[0] } else { 'main' }
$out = Join-Path (Resolve-Path '.\build') "screenshot-$name.png"

$env:LIVERELAY_DATA = (Resolve-Path '.\data')
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output "失败：进程已退出，代码 $($p.ExitCode)"; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) { Write-Output '失败：没有主窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

[void][Cap]::SetWindowPos($hwnd, $HWND_TOPMOST, 0, 0, 0, 0, $SWP)
Start-Sleep -Seconds 3

$r = New-Object Cap+RECT
[void][Cap]::GetWindowRect($hwnd, [ref]$r)
$w = $r.R - $r.L
$h = $r.B - $r.T

$bmp = New-Object System.Drawing.Bitmap $w, $h
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size)
$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose()

# 还原层级，别把用户的窗口一直压在下面
[void][Cap]::SetWindowPos($hwnd, $HWND_NOTOPMOST, 0, 0, 0, 0, $SWP)
Stop-Process -Id $p.Id -Force
Write-Output "已保存：$out（${w}x${h}）"
