param([string]$OutputPath)

$ErrorActionPreference = 'Stop'
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $OutputPath = Join-Path $projectDirectory '.run/camera-status-a2-02/camera-status-upgrade.sql'
}
$target = [IO.Path]::GetFullPath($OutputPath)
$names = @('相机状态检查表.sql', '相机状态快照表.sql')
$sections = @(
    '-- Generated create-only camera status upgrade; inspect/backup/rehearse before executing.'
    '-- Requires the current camera asset schema. Existing status tables must be assessed separately.'
    '-- No DROP, no data update, no automatic execution. MySQL DDL is not transactionally reversible.'
    'SET NAMES utf8mb4;'
    "SET time_zone = '+08:00';"
    'SET FOREIGN_KEY_CHECKS = 1;'
    ''
)
foreach ($name in $names) {
    $source = Join-Path $projectDirectory "platform-api/sql/业务/$name"
    $sql = [IO.File]::ReadAllText($source).Replace("`r`n", "`n")
    $matches = [regex]::Matches($sql, '(?ms)^CREATE TABLE camera_status_\w+ \(.*?^\) ENGINE=.*?;')
    if ($matches.Count -ne 1) { throw "Expected one status table definition: $name" }
    $sections += $matches[0].Value
    $sections += ''
}
$text = ($sections -join "`n").TrimEnd() + "`n"
if ($text -match '(?im)^\s*(DROP|ALTER|DELETE|UPDATE|INSERT)\b') {
    throw 'Unexpected destructive/data statement in generated upgrade.'
}
if (Test-Path -LiteralPath $target) {
    if ([IO.File]::ReadAllText($target).Replace("`r`n", "`n") -cne $text) {
        throw 'The output already exists with different content; choose a new output path.'
    }
} else {
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
    [IO.File]::WriteAllText($target, $text, [Text.UTF8Encoding]::new($false))
}
Write-Output "Create-only upgrade generated for review: $target"
