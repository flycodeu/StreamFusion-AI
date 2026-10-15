param([string]$OutputPath)

$ErrorActionPreference = 'Stop'
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path $projectDirectory '.run/camera-ui-05/camera-patrol-upgrade.sql'
}
$target = [IO.Path]::GetFullPath($OutputPath)
$sections = @(
    '-- Candidate upgrade ONLY when current status tables exist, plan_id is absent, and both patrol tables are absent.'
    '-- Backup and rehearse first. No data write, no DROP, no automatic execution; MySQL DDL implicitly commits.'
    'SET NAMES utf8mb4;'
    "SET time_zone = '+08:00';"
    'SET FOREIGN_KEY_CHECKS = 1;'
)
foreach ($name in @('相机巡检计划表.sql', '相机巡检目标表.sql')) {
    $source = Join-Path $projectDirectory "platform-api/sql/业务/$name"
    $sql = [IO.File]::ReadAllText($source).Replace("`r`n", "`n")
    $matches = [regex]::Matches($sql, '(?ms)^CREATE TABLE camera_status_plan(?:_target)? \(.*?^\) ENGINE=.*?;')
    if ($matches.Count -ne 1) { throw "Expected one patrol table: $name" }
    $sections += $matches[0].Value
}
$checkSource = [IO.File]::ReadAllText((Join-Path $projectDirectory 'platform-api/sql/业务/相机状态检查表.sql'))
$column = [regex]::Matches($checkSource, '(?m)^\s*(plan_id BIGINT NULL[^\r\n]*),\r?$')
if ($column.Count -ne 1) { throw 'Expected one plan_id definition in maintained check table.' }
$sections += 'ALTER TABLE camera_status_check ADD COLUMN ' + $column[0].Groups[1].Value + ';'
$text = ($sections -join "`n").TrimEnd() + "`n"
if ($text -match '(?im)^\s*(DROP|DELETE|UPDATE|INSERT|TRUNCATE)\b') { throw 'Unexpected destructive/data statement.' }
if (Test-Path -LiteralPath $target) {
    if ([IO.File]::ReadAllText($target).Replace("`r`n", "`n") -cne $text) { throw 'Output differs; choose a new output path.' }
} else {
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
    [IO.File]::WriteAllText($target, $text, [Text.UTF8Encoding]::new($false))
}
Write-Output "Patrol candidate upgrade generated for review: $target"
