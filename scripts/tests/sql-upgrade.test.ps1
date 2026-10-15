$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('sf-sql-upgrade-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporary | Out-Null
try {
    $path = Join-Path $temporary 'menu.sql'
    $generator = Join-Path $root 'scripts/export-diagnostics-menu-upgrade.ps1'
    & $generator -MonitorDirectoryId '2103793846734290945' -OutputPath $path | Out-Null
    $sql = [IO.File]::ReadAllText($path)
    if (!$sql.Contains('VALUES (1010, 2103793846734290945,') -or $sql -match '(?im)^\s*(DROP|DELETE|UPDATE|TRUNCATE)\b') { throw 'Incorrect additive menu SQL.' }
    & $generator -MonitorDirectoryId '2103793846734290945' -OutputPath $path | Out-Null
    $rejected = $false
    try { & $generator -MonitorDirectoryId '1008' -OutputPath $path | Out-Null } catch { $rejected = $true }
    if (!$rejected) { throw 'Different content overwrote an existing candidate.' }
    foreach ($id in @('0','-1','1;DELETE','9223372036854775808')) {
        $rejected = $false
        try { & $generator -MonitorDirectoryId $id -OutputPath (Join-Path $temporary 'invalid.sql') | Out-Null } catch { $rejected = $true }
        if (!$rejected) { throw 'Invalid directory ID accepted.' }
    }
    foreach ($name in @('audit-maintenance','camera-status','camera-patrol','camera-status-time')) {
        $candidate = Join-Path $temporary "$name.sql"
        & (Join-Path $root "scripts/export-$name-upgrade.ps1") -OutputPath $candidate | Out-Null
        if ([IO.File]::ReadAllText($candidate) -match '(?im)^\s*(DROP|DELETE|TRUNCATE)\b') { throw "Destructive SQL in $name candidate." }
    }
    Write-Output 'SQL upgrade generator checks passed: actual parent, idempotent generation, no destructive SQL, overwrite/invalid-ID refusal.'
} finally {
    # Only generated files inside this exact temporary directory are removed.
    $resolved = [IO.Path]::GetFullPath($temporary)
    if (!$resolved.StartsWith([IO.Path]::GetFullPath([IO.Path]::GetTempPath()), [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notmatch '^sf-sql-upgrade-[a-f0-9]{32}$') { throw 'Unexpected cleanup target.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
