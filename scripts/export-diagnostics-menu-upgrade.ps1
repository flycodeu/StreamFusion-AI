param(
    [string]$OutputPath = '.run/log-implement-01/diagnostics-menu-upgrade.sql',
    [ValidatePattern('^[1-9][0-9]{0,18}$')][string]$MonitorDirectoryId = '1008'
)

$ErrorActionPreference = 'Stop'
$parsedDirectoryId = 0L
if (![long]::TryParse($MonitorDirectoryId, [ref]$parsedDirectoryId)) { throw 'Monitor directory ID is out of range.' }
$projectDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$target = if ([IO.Path]::IsPathRooted($OutputPath)) { [IO.Path]::GetFullPath($OutputPath) } else { [IO.Path]::GetFullPath((Join-Path $projectDirectory $OutputPath)) }
$source = [IO.File]::ReadAllText((Join-Path $projectDirectory 'platform-api/sql/业务/菜单表.sql'))
$seed = [regex]::Match($source, '(?s)-- BEGIN DIAGNOSTICS MENU SEED\r?\n(.*?)-- END DIAGNOSTICS MENU SEED')
if (-not $seed.Success) { throw 'Diagnostics menu seed not found.' }
$text = @'
-- Candidate only: backup first. Requires the reviewed monitor directory and an unused menu id 1010.
-- Check id, route_name and path conflicts before executing. Never use mysql --force.
-- Stop on any error; do not replay initialization scripts or overwrite an existing menu.
SET NAMES utf8mb4;
START TRANSACTION;
'@ + "`n" + $seed.Groups[1].Value.Trim().Replace('VALUES (1010, 1008,', "VALUES (1010, $MonitorDirectoryId,") + "`n" + @'
INSERT INTO sys_role_menu(role_id, menu_id)
SELECT r.id, 1010 FROM sys_role r WHERE r.code='SUPER_ADMIN'
AND NOT EXISTS (SELECT 1 FROM sys_role_menu rm WHERE rm.role_id=r.id AND rm.menu_id=1010);
COMMIT;
'@ + "`n"
if (Test-Path -LiteralPath $target) {
    if ([IO.File]::ReadAllText($target).Replace("`r`n", "`n") -cne $text.Replace("`r`n", "`n")) { throw 'Output differs; choose a new path.' }
} else {
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
    [IO.File]::WriteAllText($target, $text, [Text.UTF8Encoding]::new($false))
}
Write-Output "Candidate generated, not executed: $target"
