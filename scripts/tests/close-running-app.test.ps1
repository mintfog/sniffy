# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

$ErrorActionPreference = 'Stop'
$testDir = Join-Path ([IO.Path]::GetTempPath()) ("sniffy 安装关闭测试 " + [Guid]::NewGuid())
$processes = [Collections.Generic.List[Diagnostics.Process]]::new()
$proxyPorts = @{}
New-Item -ItemType Directory -Path $testDir | Out-Null

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;

public static class SniffyInstallerUI {
    private delegate bool EnumWindowsProc(IntPtr window, IntPtr parameter);

    [DllImport("user32.dll")]
    private static extern bool EnumWindows(EnumWindowsProc callback, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern bool EnumChildWindows(IntPtr parent, EnumWindowsProc callback, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern uint GetWindowThreadProcessId(IntPtr window, out uint processID);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int GetWindowText(IntPtr window, StringBuilder text, int count);
    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool PostMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);

    private static string WindowText(IntPtr window) {
        var text = new StringBuilder(256);
        GetWindowText(window, text, text.Capacity);
        return text.ToString();
    }

    public static IntPtr FindPrompt(int processID) {
        IntPtr result = IntPtr.Zero;
        EnumWindows((window, parameter) => {
            uint owner;
            GetWindowThreadProcessId(window, out owner);
            if (owner != processID || WindowText(window) != "Sniffy 更新") return true;
            result = window;
            return false;
        }, IntPtr.Zero);
        return result;
    }

    public static IntPtr FindButton(IntPtr dialog, string label) {
        IntPtr result = IntPtr.Zero;
        EnumChildWindows(dialog, (window, parameter) => {
            if (WindowText(window) != label) return true;
            result = window;
            return false;
        }, IntPtr.Zero);
        return result;
    }

    private static void Post(IntPtr window, uint message, int key = 0) {
        if (!PostMessage(window, message, new IntPtr(key), IntPtr.Zero)) {
            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
        }
    }

    public static void Click(IntPtr button) { Post(button, 0x00F5); }
    public static void Close(IntPtr dialog) { Post(dialog, 0x0010); }
    public static void Escape(IntPtr dialog) {
        Post(dialog, 0x0100, 0x1B);
        Post(dialog, 0x0101, 0x1B);
    }
}
'@

function Start-TestApp([string]$Directory, [switch]$Hidden, [switch]$Listen) {
    New-Item -ItemType Directory -Path $Directory -Force | Out-Null
    $binary = Join-Path $Directory 'Sniffy.exe'
    if (-not (Test-Path $binary)) { Copy-Item $fixture $binary }
    $startArgs = @{ FilePath = $binary; PassThru = $true }
    $arguments = @()
    if ($Hidden) { $arguments += '--hidden' }
    if ($Listen) {
        $portFile = Join-Path $Directory ("proxy-port-" + [Guid]::NewGuid() + '.txt')
        $arguments += "--port-file=`"$portFile`""
    }
    if ($arguments.Count -gt 0) { $startArgs.ArgumentList = $arguments }
    $app = Start-Process @startArgs
    $processes.Add($app)
    if (-not $app.WaitForInputIdle(10000)) { throw '测试应用启动超时' }
    if ($Listen) {
        $proxyPorts[$app.Id] = [int][IO.File]::ReadAllText($portFile)
    }
    return $app
}

function Start-TestInstaller(
    [string]$Directory,
    [bool]$Silent = $true,
    [Diagnostics.Process]$Initiator,
    [long]$InitiatorStartTime = 0
) {
    # NSIS 的 /D 必须为最后一个参数，路径含空格时也不加引号。
    $arguments = "/D=$Directory"
    if ($null -ne $Initiator) {
        if ($InitiatorStartTime -eq 0) {
            $InitiatorStartTime = $Initiator.StartTime.ToUniversalTime().ToFileTimeUtc()
        }
        $low = [uint32]([uint64]$InitiatorStartTime -band [uint64]4294967295)
        $high = [uint32]([uint64]$InitiatorStartTime -shr 32)
        $arguments = "/UPDATEPID=$($Initiator.Id) /UPDATESTARTLOW=$low /UPDATESTARTHIGH=$high $arguments"
    }
    if ($Silent) { $arguments = "/S $arguments" }
    $installerProcess = Start-Process -FilePath $installer -ArgumentList $arguments -PassThru
    $processes.Add($installerProcess)
    return $installerProcess
}

function Wait-TestInstaller([Diagnostics.Process]$InstallerProcess, [string]$Directory, [int]$ExpectedExitCode = 0) {
    if (-not $installerProcess.WaitForExit(90000)) { throw '测试安装超时' }
    if ($installerProcess.ExitCode -ne $ExpectedExitCode) {
        throw "安装退出码 $($installerProcess.ExitCode)，预期 $ExpectedExitCode"
    }
    if ($ExpectedExitCode -eq 0 -and [IO.File]::ReadAllText((Join-Path $Directory 'Sniffy.exe')) -ne 'updated') {
        throw '安装未替换目标文件'
    }
}

function Install-TestUpdate([string]$Directory, [int]$ExpectedExitCode = 0) {
    $installerProcess = Start-TestInstaller $Directory
    Wait-TestInstaller $installerProcess $Directory $ExpectedExitCode
}

function Wait-TestPrompt([Diagnostics.Process]$InstallerProcess) {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    while ($timer.Elapsed.TotalSeconds -lt 20) {
        if ($InstallerProcess.HasExited) { throw '用户确认前安装程序已退出' }
        $dialog = [SniffyInstallerUI]::FindPrompt($InstallerProcess.Id)
        if ($dialog -ne [IntPtr]::Zero) {
            $confirm = [SniffyInstallerUI]::FindButton($dialog, '关闭软件并更新')
            $cancel = [SniffyInstallerUI]::FindButton($dialog, '放弃更新')
            if ($confirm -ne [IntPtr]::Zero -and $cancel -ne [IntPtr]::Zero) {
                return @{ Dialog = $dialog; Confirm = $confirm; Cancel = $cancel }
            }
        }
        Start-Sleep -Milliseconds 100
    }
    throw '未显示包含“关闭软件并更新”和“放弃更新”的确认框'
}

function Assert-OldVersionUnchanged([string]$Directory, [string]$Hash, [Diagnostics.Process[]]$Applications) {
    foreach ($app in $Applications) {
        if ($app.HasExited) { throw '用户确认前旧版本进程已退出' }
    }
    if ((Get-FileHash -LiteralPath (Join-Path $Directory 'Sniffy.exe')).Hash -ne $Hash) {
        throw '用户确认前旧版本文件已变化'
    }
}

function Assert-TestProxyPort([Diagnostics.Process]$Application, [bool]$Occupied) {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $proxyPorts[$Application.Id])
    $listener.ExclusiveAddressUse = $true
    $available = $false
    try {
        $listener.Start()
        $available = $true
    } catch [Net.Sockets.SocketException] {
        if ($_.Exception.SocketErrorCode -notin @(
            [Net.Sockets.SocketError]::AddressAlreadyInUse,
            [Net.Sockets.SocketError]::AccessDenied
        )) { throw }
    } finally {
        $listener.Stop()
    }
    if ($available -eq $Occupied) {
        throw "代理端口 $($proxyPorts[$Application.Id]) 的占用状态不符，预期占用：$Occupied"
    }
}

function Assert-TestInstallTargetUnchanged([string]$Directory, [string]$Hash) {
    $binary = Join-Path $Directory 'Sniffy.exe'
    if ($Hash) {
        if ((Get-FileHash -LiteralPath $binary).Hash -ne $Hash) { throw '用户确认前安装目录中的文件已变化' }
    } elseif (Test-Path -LiteralPath $binary) {
        throw '用户确认前安装目录中已生成程序文件'
    }
}

try {
    $source = Join-Path $testDir 'TestApp.cs'
    $fixture = Join-Path $testDir 'fixture.exe'
    $installer = Join-Path $testDir 'setup.exe'
    @'
using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Windows.Forms;
class TestApp {
    [STAThread]
    static void Main(string[] args) {
        var form = new Form { Text = "Sniffy installer test", ShowInTaskbar = false };
        TcpListener listener = null;
        foreach (string arg in args) {
            if (arg == "--hidden") form.Shown += (sender, e) => form.Hide();
            if (arg.StartsWith("--port-file=")) {
                listener = new TcpListener(IPAddress.Loopback, 0);
                listener.ExclusiveAddressUse = true;
                listener.Start();
                File.WriteAllText(arg.Substring("--port-file=".Length), ((IPEndPoint)listener.LocalEndpoint).Port.ToString());
            }
        }
        try { Application.Run(form); }
        finally { if (listener != null) listener.Stop(); }
    }
}
'@ | Set-Content -LiteralPath $source -Encoding utf8
    $compiler = Join-Path $env:WINDIR 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
    & $compiler /nologo /target:winexe /reference:System.Windows.Forms.dll "/out:$fixture" $source
    if ($LASTEXITCODE -ne 0) { throw '测试应用编译失败' }
    & makensis -V2 -INPUTCHARSET UTF8 -NOCD "-DTEST_INSTALLER=$installer" (Join-Path $PSScriptRoot 'close-running-app.nsi')
    if ($LASTEXITCODE -ne 0) { throw '测试安装包编译失败' }

    Install-TestUpdate (Join-Path $testDir '首次安装')

    $idleDir = Join-Path $testDir '已退出的旧版本'
    New-Item -ItemType Directory -Path $idleDir | Out-Null
    Copy-Item $fixture (Join-Path $idleDir 'Sniffy.exe')
    Install-TestUpdate $idleDir

    $other = Start-TestApp (Join-Path $testDir '其他安装目录')
    foreach ($silent in @($true, $false)) {
        $mode = if ($silent) { '静默安装' } else { '交互安装' }
        $runningDir = Join-Path $testDir "$mode 正在运行的旧版本"
        $first = Start-TestApp $runningDir
        $second = Start-TestApp $runningDir -Hidden
        $before = (Get-FileHash -LiteralPath (Join-Path $runningDir 'Sniffy.exe')).Hash

        foreach ($dismissal in @('按钮', '关闭', 'Escape')) {
            $installerProcess = Start-TestInstaller $runningDir $silent
            $prompt = Wait-TestPrompt $installerProcess
            Assert-OldVersionUnchanged $runningDir $before @($first, $second)
            switch ($dismissal) {
                '按钮' { [SniffyInstallerUI]::Click($prompt.Cancel) }
                '关闭' { [SniffyInstallerUI]::Close($prompt.Dialog) }
                'Escape' { [SniffyInstallerUI]::Escape($prompt.Dialog) }
            }
            Wait-TestInstaller $installerProcess $runningDir 2
            Assert-OldVersionUnchanged $runningDir $before @($first, $second)
        }

        $installerProcess = Start-TestInstaller $runningDir $silent
        $prompt = Wait-TestPrompt $installerProcess
        Assert-OldVersionUnchanged $runningDir $before @($first, $second)
        [SniffyInstallerUI]::Click($prompt.Confirm)
        Wait-TestInstaller $installerProcess $runningDir
        if (-not $first.HasExited -or -not $second.HasExited) { throw '确认更新后旧版本进程仍在运行' }
        if ($other.HasExited) { throw '其他目录中的同名程序被关闭' }
    }

    foreach ($existingTarget in @($false, $true)) {
        foreach ($silent in @($true, $false)) {
            $mode = if ($silent) { '静默安装' } else { '交互安装' }
            $targetKind = if ($existingTarget) { '已有未运行安装目录' } else { '全新安装目录' }
            $portableDir = Join-Path $testDir "$targetKind $mode 免安装目录"
            $targetDir = Join-Path $testDir "$targetKind $mode 安装目标"
            $portable = Start-TestApp $portableDir -Hidden -Listen
            $portableHash = (Get-FileHash -LiteralPath (Join-Path $portableDir 'Sniffy.exe')).Hash
            $targetHash = $null
            if ($existingTarget) {
                New-Item -ItemType Directory -Path $targetDir | Out-Null
                Copy-Item $fixture (Join-Path $targetDir 'Sniffy.exe')
                $targetHash = (Get-FileHash -LiteralPath (Join-Path $targetDir 'Sniffy.exe')).Hash
            }

            $installerProcess = Start-TestInstaller $targetDir $silent -Initiator $portable
            $prompt = Wait-TestPrompt $installerProcess
            Assert-OldVersionUnchanged $portableDir $portableHash @($portable, $other)
            Assert-TestInstallTargetUnchanged $targetDir $targetHash
            Assert-TestProxyPort $portable $true
            [SniffyInstallerUI]::Click($prompt.Cancel)
            Wait-TestInstaller $installerProcess $targetDir 2
            Assert-OldVersionUnchanged $portableDir $portableHash @($portable, $other)
            Assert-TestInstallTargetUnchanged $targetDir $targetHash
            Assert-TestProxyPort $portable $true

            $installerProcess = Start-TestInstaller $targetDir $silent -Initiator $portable
            $prompt = Wait-TestPrompt $installerProcess
            Assert-OldVersionUnchanged $portableDir $portableHash @($portable, $other)
            Assert-TestInstallTargetUnchanged $targetDir $targetHash
            Assert-TestProxyPort $portable $true
            [SniffyInstallerUI]::Click($prompt.Confirm)
            Wait-TestInstaller $installerProcess $targetDir
            if (-not $portable.HasExited) { throw '确认更新后免安装版发起进程仍在运行' }
            Assert-TestProxyPort $portable $false
            if ((Get-FileHash -LiteralPath (Join-Path $portableDir 'Sniffy.exe')).Hash -ne $portableHash) {
                throw '免安装目录中的程序文件被改写'
            }
            if ($other.HasExited) { throw '免安装版更新时关闭了其他目录中的同名程序' }
        }
    }

    $mismatchDir = Join-Path $testDir '启动时间不匹配的发起进程'
    $mismatchTarget = Join-Path $testDir '启动时间不匹配的安装目标'
    $mismatch = Start-TestApp $mismatchDir -Listen
    $before = (Get-FileHash -LiteralPath (Join-Path $mismatchDir 'Sniffy.exe')).Hash
    $incorrectStartTime = $mismatch.StartTime.ToUniversalTime().ToFileTimeUtc() + 1
    $installerProcess = Start-TestInstaller $mismatchTarget -Initiator $mismatch -InitiatorStartTime $incorrectStartTime
    Wait-TestInstaller $installerProcess $mismatchTarget 2
    Assert-OldVersionUnchanged $mismatchDir $before @($mismatch, $other)
    Assert-TestInstallTargetUnchanged $mismatchTarget $null
    Assert-TestProxyPort $mismatch $true

    $exited = Start-TestApp (Join-Path $testDir '已退出的免安装发起进程') -Listen
    $exitedStartTime = $exited.StartTime.ToUniversalTime().ToFileTimeUtc()
    $exited.Kill()
    if (-not $exited.WaitForExit(10000)) { throw '测试发起进程退出超时' }
    $exitedTarget = Join-Path $testDir '发起进程已退出的安装目标'
    $installerProcess = Start-TestInstaller $exitedTarget -Initiator $exited -InitiatorStartTime $exitedStartTime
    Wait-TestInstaller $installerProcess $exitedTarget
    Assert-TestProxyPort $exited $false
    if ($other.HasExited) { throw '发起进程已退出时关闭了其他目录中的同名程序' }

    $readonlyDir = Join-Path $testDir '只读旧版本'
    New-Item -ItemType Directory -Path $readonlyDir | Out-Null
    $readonlyFile = Join-Path $readonlyDir 'Sniffy.exe'
    Copy-Item $fixture $readonlyFile
    $before = (Get-FileHash -LiteralPath $readonlyFile).Hash
    (Get-Item -LiteralPath $readonlyFile).IsReadOnly = $true
    Install-TestUpdate $readonlyDir 2
    if ((Get-FileHash -LiteralPath $readonlyFile).Hash -ne $before) { throw '安装失败后旧文件发生变化' }

    Write-Output 'Windows 安装关闭测试通过'
} finally {
    foreach ($app in $processes) {
        if (-not $app.HasExited) { $app.Kill(); $app.WaitForExit() }
        $app.Dispose()
    }
    Remove-Item -LiteralPath $testDir -Recurse -Force
}
