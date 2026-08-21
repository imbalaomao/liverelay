$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class TrayExit {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr parent, IntPtr child, string cls, string title);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
}
"@

# 验证托盘菜单里的「退出」是否真的退出整个程序。
#
# 之前的问题：Wails 的 runtime.Quit() 内部会再跑一遍 OnBeforeClose，而那个回调
# 在开了「关闭到托盘」时返回"阻止关闭"，于是退出被自己的策略拦下，只把窗口
# 又收了一次——用户看到的就是"退出按钮点了没反应"。
#
# systray 把菜单项点击投递成 WM_COMMAND，wParam 是菜单项 ID。ID 从 1 起递增，
# **分隔符也占一个**，所以本程序的菜单是：1=显示主界面，2=分隔符，3=退出。
# 脚本先点 1 自校验（窗口该重新出现），确认 ID 方案没错，再点 3。
$WM_CLOSE = 0x0010
$WM_COMMAND = 0x0111
$MENU_SHOW = 1
$MENU_QUIT = 3

function Get-TrayWindow($procId) {
  $cur = [IntPtr]::Zero
  while ($true) {
    $cur = [TrayExit]::FindWindowEx([IntPtr]::Zero, $cur, "SystrayClass", $null)
    if ($cur -eq [IntPtr]::Zero) { return [IntPtr]::Zero }
    $tpid = 0
    [void][TrayExit]::GetWindowThreadProcessId($cur, [ref]$tpid)
    if ($tpid -eq $procId) { return $cur }
  }
}

$env:LIVERELAY_DATA = (Resolve-Path '.\data')
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output '失败：进程启动即退出'; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
$tray = Get-TrayWindow $p.Id
if ($tray -eq [IntPtr]::Zero) { Write-Output '失败：找不到托盘窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

# 收进托盘，复现用户的实际处境
[void][TrayExit]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 3
if ($p.HasExited) { Write-Output '失败：开了关闭到托盘却退出了'; exit 1 }
Write-Output '已收进托盘   : True'

# 自校验：点「显示主界面」，窗口该回来。回不来说明菜单 ID 猜错了，
# 后面点「退出」的结果也就不能作数
[void][TrayExit]::PostMessage($tray, $WM_COMMAND, [IntPtr]$MENU_SHOW, [IntPtr]::Zero)
Start-Sleep -Seconds 3
if (-not [TrayExit]::IsWindowVisible($hwnd)) {
  Write-Output '失败：点「显示主界面」没反应，菜单 ID 可能不对，本次测试无效'
  Stop-Process -Id $p.Id -Force
  exit 1
}
Write-Output '菜单可用     : True（「显示主界面」生效）'

# 再收回托盘，然后点「退出」
[void][TrayExit]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 2
[void][TrayExit]::PostMessage($tray, $WM_COMMAND, [IntPtr]$MENU_QUIT, [IntPtr]::Zero)
Start-Sleep -Seconds 8

if ($p.HasExited) {
  Write-Output '进程已退出   : True'
  Write-Output '通过：托盘菜单的「退出」能真正结束程序'
  exit 0
}

Write-Output '进程已退出   : False'
Write-Output '失败：点了「退出」程序还活着，退出被收托盘策略拦下了'
Stop-Process -Id $p.Id -Force
exit 1
