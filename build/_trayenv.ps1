# 托盘验证脚本的共享环境准备。用 pwsh 7 运行。
#
# 这些脚本以前依赖「关闭到托盘」的默认值为开启，默认改成关闭后就集体失效了。
# 现在各自显式写入所需配置，不再依赖默认值——也顺便不再动用户真实的
# data/config.json。

# 用固定名的临时目录并在开头重建，而不是随机名 + 结尾清理：
# 这些脚本里到处是 exit，收尾语句很容易变成执行不到的死代码。
function New-TrayTestData {
    param(
        [Parameter(Mandatory)][string]$Name,
        [bool]$CloseToTray = $true
    )

    $dir = Join-Path ([System.IO.Path]::GetTempPath()) "liverelay-verify-$Name"
    if (Test-Path $dir) { Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null

    $cfg = @{
        version  = 1
        settings = @{
            closeToTray      = $CloseToTray
            preventSleep     = $false
            maxConcurrent    = 4
            probeIntervalSec = 60
            theme            = 'dark'
            proxy            = @{ type = 'http'; enabled = $false }
        }
        tools    = @()
        tasks    = @()
    } | ConvertTo-Json -Depth 6

    # 写成不带 BOM 的 UTF-8。程序虽然容忍 BOM（parse 会剥掉），但没必要自找麻烦。
    [System.IO.File]::WriteAllText((Join-Path $dir 'config.json'), $cfg, [System.Text.UTF8Encoding]::new($false))
    return $dir
}

# 找到属于指定进程的那个 systray 隐藏窗口。
# 机器上常有多个程序在用 systray，按类名找会拿到别人的。
function Get-OwnedTrayWindow {
    param([Parameter(Mandatory)][int]$ProcId)
    $cur = [IntPtr]::Zero
    while ($true) {
        $cur = [TrayWin]::FindWindowEx([IntPtr]::Zero, $cur, "SystrayClass", $null)
        if ($cur -eq [IntPtr]::Zero) { return [IntPtr]::Zero }
        $tpid = 0
        [void][TrayWin]::GetWindowThreadProcessId($cur, [ref]$tpid)
        if ($tpid -eq $ProcId) { return $cur }
    }
}
