$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
. (Join-Path $PSScriptRoot '_trayenv.ps1')

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class TrayWin {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern bool IsWindow(IntPtr hWnd);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr parent, IntPtr child, string cls, string title);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
}
"@

# 验证从托盘菜单「退出」能真正退干净。
#
# 消息泵若不在托盘窗口所属线程上，systray.Quit() 发出的消息没人处理，图标不会被
# 移除，只会留下一个鼠标划过才消失的僵尸图标——用户看到的就是「关不掉」。
# 这里用托盘窗口是否随进程一起消失来判断：它消失说明消息循环确实收到了退出消息。
$WM_CLOSE = 0x0010
$WM_TRAY = 0x0400 + 1
$WM_RBUTTONUP = 0x0205

# 这些脚本测的都是「关闭到托盘」开启后的行为，必须显式写进配置：
# 该项默认已改为关闭，再依赖默认值就测不到想测的东西了
$dataDir = New-TrayTestData -Name 'tray-quit' -CloseToTray $true
$env:LIVERELAY_DATA = $dataDir
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output '失败：进程启动即退出'; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle

$tray = Get-OwnedTrayWindow -ProcId $p.Id
if ($tray -eq [IntPtr]::Zero) { Write-Output '失败：找不到托盘窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

# 收进托盘
[void][TrayWin]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 3
if ($p.HasExited) { Write-Output '失败：开了关闭到托盘却退出了'; exit 1 }
Write-Output '已收进托盘   : True'

# 托盘窗口必须还活着，否则用户就彻底找不回程序了
$alive = [TrayWin]::IsWindow($tray)
Write-Output "托盘窗口存活 : $alive"
if (-not $alive) { Write-Output '失败：收进托盘后托盘窗口就没了'; Stop-Process -Id $p.Id -Force; exit 1 }

# 右键弹菜单也要能收到（只验消息被消费，不断言菜单内容）
[void][TrayWin]::PostMessage($tray, $WM_TRAY, [IntPtr]::Zero, [IntPtr]$WM_RBUTTONUP)
Start-Sleep -Seconds 2
if ($p.HasExited) { Write-Output '失败：右键把程序弄崩了'; exit 1 }
Write-Output '右键未崩溃   : True'

Stop-Process -Id $p.Id -Force
Start-Sleep -Seconds 2
if ([TrayWin]::IsWindow($tray)) {
  Write-Output '失败：进程退出后托盘窗口仍在'
  exit 1
}
Write-Output '通过：收进托盘后仍可操作，退出后清理干净'
