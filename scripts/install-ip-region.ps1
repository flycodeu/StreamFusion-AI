[CmdletBinding()]
param(
    [string]$OutputDirectory,
    [ValidateRange(5, 300)]
    [int]$TimeoutSeconds = 90
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.Net.Http
if ([string]::IsNullOrEmpty($OutputDirectory)) {
    $OutputDirectory = Join-Path (Split-Path $PSScriptRoot -Parent) 'platform-api/.run/ip-region'
}

$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'ip-region.manifest.json') -Raw | ConvertFrom-Json
$expectedNames = @('ip2region_v4.xdb', 'ip2region_v6.xdb', 'LICENSE.md')
if ($manifest.repository -ne 'https://github.com/lionsoul2014/ip2region' -or
    $manifest.commit -notmatch '^[0-9a-f]{40}$' -or
    @($manifest.files).Count -ne $expectedNames.Count) {
    throw 'Invalid pinned ip2region manifest.'
}
$seenNames = @{}
[long]$databaseBytes = 0
foreach ($artifact in $manifest.files) {
    if ($artifact.name -notin $expectedNames -or $seenNames.ContainsKey($artifact.name) -or
        $artifact.sha256 -notmatch '^[0-9a-f]{64}$' -or
        $artifact.size -le 0 -or $artifact.size -gt 128MB) {
        throw 'Invalid ip2region artifact manifest.'
    }
    $seenNames[$artifact.name] = $true
    $expectedPath = if ($artifact.name -eq 'LICENSE.md') { 'LICENSE.md' } else { 'data/' + $artifact.name }
    if ($artifact.path -ne $expectedPath) { throw 'Unexpected ip2region source path.' }
    if ($artifact.name -ne 'LICENSE.md') { $databaseBytes += [long]$artifact.size }
}
if ($databaseBytes -gt 128MB -or $databaseBytes -gt [long]$manifest.maxDatabaseBytes) {
    throw 'The combined ip2region databases exceed the 128 MiB limit.'
}

function Assert-Artifact {
    param([string]$Path, [object]$Artifact)
    $item = Get-Item -LiteralPath $Path -Force
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing to replace a directory or linked file: $Path"
    }
    if ($item.Length -ne [long]$Artifact.size) { throw "Size mismatch; preserving file: $Path" }
    if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash -ne $Artifact.sha256) {
        throw "SHA256 mismatch; preserving file: $Path"
    }
    if ($Artifact.ipVersion) {
        $stream = [IO.File]::OpenRead($Path)
        try {
            $header = New-Object byte[] 256
            $count = $stream.Read($header, 0, $header.Length)
            if ($count -ne 256 -or
                [Text.Encoding]::ASCII.GetString($header).StartsWith('version https://git-lfs.github.com/spec/v1') -or
                [BitConverter]::ToUInt16($header, 0) -ne 3 -or
                [BitConverter]::ToUInt16($header, 16) -ne [int]$Artifact.ipVersion) {
                throw "Invalid XDB header or LFS pointer: $Path"
            }
        } finally {
            $stream.Dispose()
        }
    }
}

$destination = [IO.Path]::GetFullPath($OutputDirectory)
$pending = @()
# Check every existing file before downloading anything. Custom databases are never overwritten.
foreach ($artifact in $manifest.files) {
    $target = Join-Path $destination $artifact.name
    if (Test-Path -LiteralPath $target) {
        Assert-Artifact -Path $target -Artifact $artifact
        Write-Host "Verified existing $($artifact.name); download skipped."
    } else {
        $pending += $artifact
    }
}
if ($pending.Count -eq 0) {
    Write-Host "All pinned ip2region $($manifest.tag) files are installed in $destination"
    return
}
New-Item -ItemType Directory -Path $destination -Force | Out-Null

$handler = New-Object System.Net.Http.HttpClientHandler
$handler.AllowAutoRedirect = $false
$client = New-Object System.Net.Http.HttpClient($handler)
$client.DefaultRequestHeaders.UserAgent.ParseAdd('StreamFusion-IPRegion-Installer/1.0')
try {
    foreach ($artifact in $pending) {
        $target = Join-Path $destination $artifact.name
        $temporary = Join-Path $destination ('.' + $artifact.name + '.' + [guid]::NewGuid().ToString('N') + '.partial')
        $uri = 'https://raw.githubusercontent.com/lionsoul2014/ip2region/' + $manifest.commit + '/' + $artifact.path
        $cancellation = New-Object System.Threading.CancellationTokenSource
        $cancellation.CancelAfter($TimeoutSeconds * 1000)
        $response = $null
        $inputStream = $null
        $outputStream = $null
        $request = New-Object System.Net.Http.HttpRequestMessage([System.Net.Http.HttpMethod]::Get, $uri)
        try {
            Write-Host "Downloading $($artifact.name) from pinned $($manifest.tag)..."
            $response = $client.SendAsync($request, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $cancellation.Token).GetAwaiter().GetResult()
            [void]$response.EnsureSuccessStatusCode()
            $length = $response.Content.Headers.ContentLength
            if ($null -ne $length -and $length -ne [long]$artifact.size) { throw 'Unexpected download size.' }
            $inputStream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
            $outputStream = [IO.File]::Open($temporary, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
            $buffer = New-Object byte[] 65536
            [long]$received = 0
            while ($true) {
                $count = $inputStream.ReadAsync($buffer, 0, $buffer.Length, $cancellation.Token).GetAwaiter().GetResult()
                if ($count -eq 0) { break }
                $received += $count
                if ($received -gt [long]$artifact.size) { throw 'Download exceeds its pinned size limit.' }
                $outputStream.Write($buffer, 0, $count)
            }
            $outputStream.Dispose()
            $outputStream = $null
            Assert-Artifact -Path $temporary -Artifact $artifact
            # The two-argument move fails if another process created the destination meanwhile.
            [IO.File]::Move($temporary, $target)
            Write-Host "Installed $($artifact.name) ($received bytes; SHA256 verified)."
        } finally {
            if ($null -ne $outputStream) { $outputStream.Dispose() }
            if ($null -ne $inputStream) { $inputStream.Dispose() }
            if ($null -ne $response) { $response.Dispose() }
            $request.Dispose()
            $cancellation.Dispose()
            if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
        }
    }
} finally {
    $client.Dispose()
    $handler.Dispose()
}
Write-Host "Installed pinned ip2region $($manifest.tag) files in $destination"
