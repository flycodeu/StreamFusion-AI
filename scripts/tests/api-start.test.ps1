param([string]$JavaHome = $env:JAVA_HOME)

$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    Write-Output 'SKIP API startup compatibility check requires Windows PowerShell.'
    return
}
if (!$JavaHome -or !(Test-Path -LiteralPath (Join-Path $JavaHome 'bin/java.exe'))) {
    throw 'Pass -JavaHome with a JDK 21 installation.'
}

$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$sandbox = [IO.Path]::GetFullPath((Join-Path $temporaryRoot ('streamfusion-api-start-' + [guid]::NewGuid().ToString('N'))))
if (!$sandbox.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Startup test directory escaped the temporary root.'
}
try {
    $scripts = New-Item -ItemType Directory -Path (Join-Path $sandbox 'scripts')
    $api = New-Item -ItemType Directory -Path (Join-Path $sandbox 'platform-api')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '../start.ps1') -Destination $scripts.FullName
    $wrapper = Join-Path $sandbox 'verify.ps1'
    $probe = @'
param([string]$JavaHome)
$ErrorActionPreference = 'Stop'
$env:JAVA_HOME = 'preserve-original-java-home'
$originalPath = $env:Path
$originalLocation = (Get-Location).Path
$maven = Join-Path $PSScriptRoot 'platform-api/mvnw.cmd'
try {
    [IO.File]::WriteAllText($maven, "@echo off`r`necho API_START_PROBE %*`r`nexit /b 0`r`n")
    & (Join-Path $PSScriptRoot 'scripts/start.ps1') api -JavaHome $JavaHome -Profile local
    if ($env:JAVA_HOME -ne 'preserve-original-java-home' -or $env:Path -ne $originalPath -or (Get-Location).Path -ne $originalLocation) {
        throw 'Successful startup did not restore its environment.'
    }
    [IO.File]::WriteAllText($maven, "@echo off`r`nexit /b 7`r`n")
    $failed = $false
    try { & (Join-Path $PSScriptRoot 'scripts/start.ps1') api -JavaHome $JavaHome -Profile local }
    catch {
        if ($_.Exception.Message -notmatch 'api exited with code 7') { throw }
        $failed = $true
    }
    if (!$failed) { throw 'Failed Maven startup was reported as successful.' }
    if ($env:JAVA_HOME -ne 'preserve-original-java-home' -or $env:Path -ne $originalPath -or (Get-Location).Path -ne $originalLocation) {
        throw 'Failed startup did not restore its environment.'
    }
    Write-Output 'API_START_FAILURE_AND_ENVIRONMENT_PASS'
    exit 0
} catch {
    Write-Error $_
    exit 1
}
'@
    [IO.File]::WriteAllText($wrapper, $probe, [Text.UTF8Encoding]::new($false))
    $output = & powershell.exe -NoProfile -NonInteractive -File $wrapper -JavaHome $JavaHome 2>&1
    if ($LASTEXITCODE -ne 0) { throw ($output | Out-String) }
    $text = $output | Out-String
    if ($text -notmatch 'API_START_PROBE spring-boot:run -Dspring-boot.run.profiles=local' -or
        $text -notmatch 'API_START_FAILURE_AND_ENVIRONMENT_PASS') {
        throw "Unexpected startup probe result: $text"
    }
    Write-Output 'PASS Windows PowerShell API startup: JDK stderr, Maven dispatch, failed exit and environment restoration.'
} finally {
    if (Test-Path -LiteralPath $sandbox) { Remove-Item -LiteralPath $sandbox -Recurse -Force }
}
