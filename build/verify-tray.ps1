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

# 验证「关闭到托盘」：收到 WM_CLOSE 后进程要活着、窗口要隐藏。
$WM_CLOSE = 0x0010

# 本脚本测的是该开关打开后的行为，必须显式写进配置：
# 它默认已改为关闭，再依赖默认值就测不到想测的东西了
$dataDir = New-TrayTestData -Name 'tray' -CloseToTray $true
$env:LIVERELAY_DATA = $dataDir

$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output "失败：进程启动即退出，代码 $($p.ExitCode)"; exit 1 }

$hwnd = (Get-Process -Id $p.Id).MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) { Write-Output '失败：没有主窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

Write-Output ('关闭前窗口可见 : ' + [TrayWin]::IsWindowVisible($hwnd))

$tray = Get-OwnedTrayWindow -ProcId $p.Id
Write-Output ('托盘窗口已建立 : ' + ($tray -ne [IntPtr]::Zero))
if ($tray -eq [IntPtr]::Zero) { Write-Output '失败：找不到托盘窗口'; Stop-Process -Id $p.Id -Force; exit 1 }

# 模拟点窗口的关闭按钮
[void][TrayWin]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
Start-Sleep -Seconds 3

if ($p.HasExited) { Write-Output '失败：开了关闭到托盘，进程却退出了'; exit 1 }
Write-Output '关闭后进程存活 : True'

$visible = [TrayWin]::IsWindowVisible($hwnd)
Write-Output "关闭后窗口可见 : $visible"
Stop-Process -Id $p.Id -Force

if ($visible) { Write-Output '失败：窗口没有隐藏'; exit 1 }
Write-Output '通过：关闭到托盘生效（进程存活，窗口隐藏）'
