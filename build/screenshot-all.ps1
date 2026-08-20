$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()

Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Shot {
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int cx, int cy, uint flags);
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint f, int dx, int dy, uint d, IntPtr extra);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
}
"@

# 把三个页面各截一张。侧栏导航按钮的位置是固定的，直接点过去即可。
# 需要 pwsh 7 运行（5.1 会把中文注释按 ANSI 读坏）。
$HWND_TOPMOST = [IntPtr]-1
$HWND_NOTOPMOST = [IntPtr]-2
$SWP = 0x0001 -bor 0x0002 -bor 0x0010
$LEFTDOWN = 0x0002
$LEFTUP = 0x0004

$buildDir = Resolve-Path '.\build'
$env:LIVERELAY_DATA = (Resolve-Path '.\data')
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output "失败：进程已退出，代码 $($p.ExitCode)"; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) { Write-Output '失败：没有主窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

[void][Shot]::SetWindowPos($hwnd, $HWND_TOPMOST, 0, 0, 0, 0, $SWP)
Start-Sleep -Seconds 2

$r = New-Object Shot+RECT
[void][Shot]::GetWindowRect($hwnd, [ref]$r)
$w = $r.R - $r.L
$h = $r.B - $r.T

function Save-Shot($name) {
  $bmp = New-Object System.Drawing.Bitmap $w, $h
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.L, $r.T, 0, 0, $bmp.Size)
  $path = Join-Path $buildDir "screenshot-$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)
  $g.Dispose(); $bmp.Dispose()
  Write-Output "已保存：screenshot-$name.png"
}

function Click-At($x, $y) {
  [void][Shot]::SetCursorPos($r.L + $x, $r.T + $y)
  Start-Sleep -Milliseconds 250
  [Shot]::mouse_event($LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 60
  [Shot]::mouse_event($LEFTUP, 0, 0, 0, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 900
}

Save-Shot 'tasks'

# 侧栏三个导航按钮的纵向位置
Click-At 104 156   # 内核
Save-Shot 'tools'

Click-At 104 206   # 设置
Save-Shot 'settings'

Click-At 104 106   # 回到任务页，再打开新建任务弹窗
Start-Sleep -Milliseconds 400
Click-At 1022 91   # ＋ 新建任务
Save-Shot 'task-modal'

[void][Shot]::SetWindowPos($hwnd, $HWND_NOTOPMOST, 0, 0, 0, 0, $SWP)
Stop-Process -Id $p.Id -Force
Write-Output '完成'
