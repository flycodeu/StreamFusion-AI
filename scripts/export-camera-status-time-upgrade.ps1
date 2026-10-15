param([string]$OutputPath = '.run/camera-status-10/camera-status-time-upgrade.sql')

$ErrorActionPreference = 'Stop'
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$target = if ([IO.Path]::IsPathRooted($OutputPath)) { [IO.Path]::GetFullPath($OutputPath) } else { [IO.Path]::GetFullPath((Join-Path $projectDirectory $OutputPath)) }
$source = [IO.File]::ReadAllText((Join-Path $projectDirectory 'platform-api/sql/业务/相机状态快照表.sql'))
$sections = @(
    '-- Candidate ONLY for an existing camera_status_snapshot missing all three timeline columns.'
    '-- Backup, verify actual columns and rehearse first. No automatic execution or data backfill.'
    '-- MySQL DDL implicitly commits. Do not replay after partial success; inspect actual schema.'
    'SET NAMES utf8mb4;'
    "SET time_zone = '+08:00';"
)
$columns = foreach ($name in @('state_since', 'last_accessible_at', 'last_failed_at')) {
    $definition = [regex]::Matches($source, "(?m)^\s*($name DATETIME\(3\) NULL[^\r\n]*),\r?$")
    if ($definition.Count -ne 1) { throw "Expected one maintained column: $name" }
    'ADD COLUMN ' + $definition[0].Groups[1].Value
}
$sections += 'ALTER TABLE camera_status_snapshot ' + ($columns -join ', ') + ';'
$text = ($sections -join "`n") + "`n"
if (Test-Path -LiteralPath $target) {
    if ([IO.File]::ReadAllText($target).Replace("`r`n", "`n") -cne $text) { throw 'Output differs; choose a new path.' }
} else {
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
    [IO.File]::WriteAllText($target, $text, [Text.UTF8Encoding]::new($false))
}
Write-Output "Timeline candidate upgrade generated for review: $target"
