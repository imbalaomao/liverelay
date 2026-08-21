$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()

Add-Type @"
using System;
using System.Runtime.InteropServices;
public class TrayWin {
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hWnd);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindowEx(IntPtr parent, IntPtr child, string cls, string title);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
}
"@

# 验证「全新安装时开关默认全关」，以及退出时托盘图标被正确移除。
#
# 这里刻意不预写 config.json：要测的就是程序自己生成的那份默认配置。
# 默认关闭意味着点窗口的叉应当直接退出整个程序，而不是收进托盘。
$WM_CLOSE = 0x0010

$dataDir = Join-Path ([System.IO.Path]::GetTempPath()) 'liverelay-verify-defaults'
if (Test-Path $dataDir) { Remove-Item -Recurse -Force $dataDir }
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null

$log = Join-Path $dataDir 'stderr.log'
$env:LIVERELAY_DATA = $dataDir
$p = Start-Process -FilePath '.\build\bin\LiveRelay.exe' -PassThru -RedirectStandardError $log
Start-Sleep -Seconds 12
Remove-Item Env:\LIVERELAY_DATA

if ($p.HasExited) { Write-Output "失败：进程启动即退出，代码 $($p.ExitCode)"; exit 1 }
$hwnd = (Get-Process -Id $p.Id).MainWindowHandle

# 点窗口的叉。默认关闭「关闭到托盘」，所以应当真正退出
[void][TrayWin]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)
for ($i = 0; $i -lt 24; $i++) {
    Start-Sleep -Milliseconds 500
    if ($p.HasExited) { break }
}

if (-not $p.HasExited) {
    Write-Output '失败：点叉后程序没退出——「关闭到托盘」应默认关闭'
    Stop-Process -Id $p.Id -Force
    exit 1
}
Write-Output '点叉即退出     : True'

# 程序生成的默认配置，逐项确认开关都是关的
$cfgPath = Join-Path $dataDir 'config.json'
if (-not (Test-Path $cfgPath)) { Write-Output '失败：没有生成 config.json'; exit 1 }
$cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json

$fail = $false
foreach ($item in @(
        @{ n = '关闭到托盘'; v = $cfg.settings.closeToTray },
        @{ n = '阻止休眠'; v = $cfg.settings.preventSleep },
        @{ n = '启用代理'; v = $cfg.settings.proxy.enabled })) {
    Write-Output ("默认 {0,-10}: {1}" -f $item.n, $item.v)
    if ($item.v) { $fail = $true }
}
if ($fail) { Write-Output '失败：有开关默认是打开的'; exit 1 }

# 托盘图标必须在进程退出前被移除，否则托盘里会留下一个
# 鼠标划过才消失的僵尸图标
$logText = if (Test-Path $log) { Get-Content $log -Raw } else { '' }
if ($logText -match '托盘图标已移除') {
    Write-Output '托盘图标已移除 : True'
}
elseif ($logText -match '等待托盘退出超时') {
    Write-Output '失败：等待托盘退出超时，图标会残留'
    exit 1
}
else {
    Write-Output '失败：没有看到托盘收尾日志，无法判定图标是否被移除'
    Write-Output $logText
    exit 1
}

Write-Output '通过：开关默认全关，且退出时托盘图标已清理'
