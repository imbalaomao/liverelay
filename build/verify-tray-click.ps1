$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Tray {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr parent, IntPtr child, string cls, string title);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
}
"@

# 验证「关闭到托盘」之后托盘图标还能不能用。
#
# systray 在 Windows 上把托盘图标的点击投递成一条自定义消息（WM_USER+1），
# lParam 带着真实的鼠标事件。直接给它的隐藏窗口发这条消息，等价于用户点了图标——
# 如果消息泵跑在正确的线程上，主窗口会重新显示；否则石沉大海。
$WM_CLOSE = 0x0010
$WM_TRAY = 0x0400 + 1   # WM_USER + 1
$WM_LBUTTONUP = 0x0202

$env:LIVERELAY_DATA = (Resolve-Path '.\data')
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output "失败：进程启动即退出"; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) { Write-Output '失败：没有主窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

# 找到本进程名下的 systray 隐藏窗口
$tray = [IntPtr]::Zero
$cur = [IntPtr]::Zero
while ($true) {
  $cur = [Tray]::FindWindowEx([IntPtr]::Zero, $cur, "SystrayClass", $null)
  if ($cur -eq [IntPtr]::Zero) { break }
  $tpid = 0
  [void][Tray]::GetWindowThreadProcessId($cur, [ref]$tpid)
  if ($tpid -eq $p.Id) { $tray = $cur; break }
}
if ($tray -eq [IntPtr]::Zero) { Write-Output '失败：找不到托盘窗口'; Stop-Process -Id $p.Id -Force; exit 1 }
Write-Output "托盘窗口     : 已找到"

# 1) 关闭主窗口，应收进托盘
[void][Tray]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 3
if ($p.HasExited) { Write-Output '失败：开了关闭到托盘却退出了'; exit 1 }
$hidden = -not [Tray]::IsWindowVisible($hwnd)
Write-Output "关闭后已隐藏 : $hidden"
if (-not $hidden) { Write-Output '失败：窗口没有隐藏'; Stop-Process -Id $p.Id -Force; exit 1 }

# 2) 模拟左键点击托盘图标，主窗口应重新出现
[void][Tray]::PostMessage($tray, $WM_TRAY, [IntPtr]::Zero, [IntPtr]$WM_LBUTTONUP)
Start-Sleep -Seconds 3
$shown = [Tray]::IsWindowVisible($hwnd)
Write-Output "点击图标后可见: $shown"

Stop-Process -Id $p.Id -Force
Start-Sleep -Seconds 1

if (-not $shown) {
  Write-Output '失败：点了托盘图标没有反应，消息泵多半没跑在托盘窗口所属线程上'
  exit 1
}
Write-Output '通过：托盘图标可响应点击'
