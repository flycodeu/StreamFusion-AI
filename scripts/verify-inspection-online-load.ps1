[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$StorageWeed,
    [Parameter(Mandatory = $true)]
    [string]$MediaRoot,
    [string]$EvidenceRoot = (Join-Path (Get-Location) ('.run/p0-f03/online-load-' + [guid]::NewGuid().ToString('N'))),
    [string]$JavaHome = $env:JAVA_HOME
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$platform = Join-Path $repo 'platform-api'
$signal = Join-Path $EvidenceRoot 'storage-start.signal'
$doneSignal = Join-Path $EvidenceRoot 'storage-done.signal'
$result = Join-Path $EvidenceRoot 'online-load.json'
New-Item -ItemType Directory -Force -Path $EvidenceRoot | Out-Null

if (-not $JavaHome) { throw 'JDK 21 is required. Pass -JavaHome or set JAVA_HOME.' }
$java = Join-Path $JavaHome 'bin/java.exe'
if (-not (Test-Path -LiteralPath $java)) { throw "JDK 21 java.exe not found: $java" }
$version = (& $java -version 2>&1 | Out-String)
if ($version -notmatch 'version "21(?:\.|\")') { throw "JDK 21 is required: $version" }

$oldJavaHome = $env:JAVA_HOME
$oldPath = $env:Path
$oldSignal = $env:SF_INSPECTION_ONLINE_LOAD_SIGNAL
$oldResult = $env:SF_INSPECTION_ONLINE_LOAD_RESULT
$storage = $null
$maven = $null
try {
    $env:JAVA_HOME = (Resolve-Path $JavaHome).Path
    $env:Path = "$($env:JAVA_HOME)\bin;$oldPath"
    $env:SF_INSPECTION_ONLINE_LOAD_SIGNAL = $signal
    $env:SF_INSPECTION_ONLINE_LOAD_RESULT = $result

    $maven = Start-Process -FilePath (Join-Path $platform 'mvnw.cmd') `
        -WorkingDirectory $platform `
        -ArgumentList @('-q', '-Dtest=InspectionOnlineLoadIntegrationTest', '-Dspotless.check.skip=true', 'test') `
        -RedirectStandardOutput (Join-Path $EvidenceRoot 'maven-stdout.log') `
        -RedirectStandardError (Join-Path $EvidenceRoot 'maven-stderr.log') `
        -PassThru -WindowStyle Hidden

    $readyUntil = (Get-Date).AddMinutes(3)
    while (-not (Test-Path -LiteralPath $signal)) {
        if ($maven.HasExited) { throw "online load test exited before storage phase: $($maven.ExitCode)" }
        if ((Get-Date) -gt $readyUntil) { throw 'online load test did not enter storage phase' }
        Start-Sleep -Milliseconds 250
    }

    $storageRoot = Join-Path $EvidenceRoot 'storage'
    New-Item -ItemType Directory -Force -Path $storageRoot | Out-Null
    $storage = Start-Process -FilePath 'uv' -WorkingDirectory (Join-Path $repo 'camera-inspector') `
        -ArgumentList @('run', '--locked', 'python', 'scripts/verify-storage.py', '--weed', ('"' + $StorageWeed + '"'), '--media-root', ('"' + $MediaRoot + '"')) `
        -RedirectStandardOutput (Join-Path $storageRoot 'stdout.log') `
        -RedirectStandardError (Join-Path $storageRoot 'stderr.log') `
        -PassThru -WindowStyle Hidden

    $storage.WaitForExit()
    Set-Content -LiteralPath $doneSignal -Value ([DateTimeOffset]::UtcNow.ToString('O'))
    if ($storage.ExitCode -ne 0) { throw "storage matrix failed: $($storage.ExitCode)" }
    $maven.WaitForExit()
    if ($maven.ExitCode -ne 0) { throw "online load test failed: $($maven.ExitCode)" }
    if (-not (Test-Path -LiteralPath $result)) { throw "missing online load evidence: $result" }
    Write-Output (Get-Content -Raw -LiteralPath $result)
}
finally {
    if ($maven -and -not $maven.HasExited) { Stop-Process -Id $maven.Id -Force }
    if ($storage -and -not $storage.HasExited) { Stop-Process -Id $storage.Id -Force }
    $env:JAVA_HOME = $oldJavaHome
    $env:Path = $oldPath
    $env:SF_INSPECTION_ONLINE_LOAD_SIGNAL = $oldSignal
    $env:SF_INSPECTION_ONLINE_LOAD_RESULT = $oldResult
}
