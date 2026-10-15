param([string]$OutputPath = '.run/log-implement-02/audit-maintenance-upgrade.sql')

$ErrorActionPreference = 'Stop'
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$target = if ([IO.Path]::IsPathRooted($OutputPath)) { [IO.Path]::GetFullPath($OutputPath) } else { [IO.Path]::GetFullPath((Join-Path $projectDirectory $OutputPath)) }
$sections = @(
    '-- Additive candidate ONLY when both audit-maintenance tables are absent.'
    '-- Backup and rehearse first. No connection, deletion, or automatic execution.'
    '-- MySQL DDL commits implicitly. Stop on errors; never use mysql --force.'
    'SET NAMES utf8mb4;'
    "SET time_zone = '+08:00';"
)
foreach ($name in @('审计保留策略表.sql','审计导出任务表.sql')) {
    $source = [IO.File]::ReadAllText((Join-Path $projectDirectory "platform-api/sql/业务/$name")).Replace("`r`n","`n")
    $sections += [regex]::Replace($source,'(?m)^(?:SET NAMES|DROP TABLE)[^\r\n]*\n?','').Trim()
}
$text = ($sections -join "`n") + "`n"
if ($text -match '(?im)^\s*(DROP|DELETE|UPDATE|TRUNCATE)\b') { throw 'Unexpected destructive statement.' }
if (Test-Path -LiteralPath $target) {
    if ([IO.File]::ReadAllText($target).Replace("`r`n","`n") -cne $text) { throw 'Output differs; choose a new path.' }
} else {
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
    [IO.File]::WriteAllText($target,$text,[Text.UTF8Encoding]::new($false))
}
Write-Output "Audit maintenance upgrade generated, not executed: $target"
