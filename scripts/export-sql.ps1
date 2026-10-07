param([switch]$Check)

$ErrorActionPreference = 'Stop'
$sqlDirectory = Join-Path $PSScriptRoot '../platform-api/sql'
$businessDirectory = Join-Path $sqlDirectory '业务'
$aggregateDirectory = Join-Path $sqlDirectory '汇总'
# Dependency order only; table definitions and seed data live in these files.
$names = @('部门表.sql', '用户表.sql', '角色表.sql', '菜单表.sql',
    '用户角色关联表.sql', '用户部门关联表.sql', '角色菜单关联表.sql', '操作审计表.sql', 'IP封禁表.sql', '登录记录表.sql',
    '相机来源表.sql', '相机凭据表.sql', '相机端点表.sql', '视频分组表.sql',
    '相机设备表.sql', '相机通道表.sql', '相机码流表.sql', '相机码流定位表.sql', '相机创建回执表.sql',
    '账户相机范围表.sql', '账户视频组授权表.sql', '账户相机授权表.sql', '相机接入任务表.sql', '相机导入明细表.sql')
$actual = @(Get-ChildItem -LiteralPath $businessDirectory -Filter '*.sql' |
    Select-Object -ExpandProperty Name)
if (Compare-Object $names $actual) { throw 'Register every table file in dependency order before exporting.' }

$sections = @(
    'SET NAMES utf8mb4;'
    "SET time_zone = '+08:00';"
    'SET FOREIGN_KEY_CHECKS = 1;'
    ''
)
$drops = @()
$bodies = @()
$deferred = @()
$created = @{}
foreach ($name in $names) {
    $sql = [IO.File]::ReadAllText((Join-Path $businessDirectory $name)).Replace("`r`n", "`n")
    $parts = $sql -split '(?m)^-- DEFERRED CONSTRAINT\s*$', 2
    $sql = $parts[0]
    if ($parts.Count -gt 1) { $deferred += $parts[1].Trim() }
    # Each source has one DROP and one CREATE; preserve indexes, comments and seeds.
    $drop = [regex]::Matches($sql, '(?m)^DROP TABLE IF EXISTS \x60?(\w+)\x60?;\r?$')
    $create = [regex]::Matches($sql, '(?m)^CREATE TABLE (\w+)')
    if ($drop.Count -ne 1 -or $create.Count -ne 1 -or
        $drop[0].Groups[1].Value -cne $create[0].Groups[1].Value -or
        $drop[0].Index -gt $create[0].Index) { throw "Expected one matching DROP/CREATE: $name" }
    $table = $create[0].Groups[1].Value
    if ($created.ContainsKey($table)) { throw "Duplicate table: $table" }
    foreach ($reference in [regex]::Matches($sql, 'REFERENCES (\w+)')) {
        $parent = $reference.Groups[1].Value
        if ($parent -cne $table -and -not $created.ContainsKey($parent)) {
            throw "Wrong dependency order: $table references $parent"
        }
    }
    $created[$table] = $true
    $drops = @($drop[0].Value.Trim()) + $drops
    $bodies += "-- $name"
    $bodies += $sql.Substring($create[0].Index).TrimEnd()
    $bodies += ''

}
$deferredText = ($deferred -join "`n")
foreach ($reference in [regex]::Matches($deferredText, 'REFERENCES (\w+)')) {
    if (-not $created.ContainsKey($reference.Groups[1].Value)) { throw 'Unknown deferred FK target.' }
}
# Only the channel/default-profile cycle needs a deferred edge. Keep FK checks enabled.
$sections += @'
-- BEGIN MYSQL DEFERRED DROP
SET @camera_default_fk = (SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE CONSTRAINT_SCHEMA=DATABASE() AND TABLE_NAME='camera_channel' AND CONSTRAINT_NAME='fk_camera_default_profile');
SET @camera_drop_sql = IF(@camera_default_fk > 0, 'ALTER TABLE camera_channel DROP FOREIGN KEY fk_camera_default_profile', 'SELECT 1');
PREPARE camera_drop_statement FROM @camera_drop_sql;
EXECUTE camera_drop_statement;
DEALLOCATE PREPARE camera_drop_statement;
-- END MYSQL DEFERRED DROP
'@
$sections += $drops
$sections += ''
$sections += $bodies
$sections += $deferred
$expected = (($sections -join "`n").TrimEnd() + "`n").Replace("`r`n", "`n")
$target = Join-Path $aggregateDirectory 'streamfusion-mysql.sql'
if ($Check) {
    if (-not (Test-Path -LiteralPath $target) -or
        [IO.File]::ReadAllText($target).Replace("`r`n", "`n") -cne $expected) {
        throw 'streamfusion-mysql.sql is stale. Run scripts/export-sql.ps1.'
    }
} else {
    [IO.Directory]::CreateDirectory($aggregateDirectory) | Out-Null
    [IO.File]::WriteAllText($target, $expected, [Text.UTF8Encoding]::new($false))
}

# Current upgrade only. Earlier dated migrations remain frozen historical evidence;
# do not regenerate them from a newer schema or guess an old database's shape.
function Read-ColumnDefinition([string]$file, [string]$column) {
    $source = [IO.File]::ReadAllText((Join-Path $businessDirectory $file))
    $match = [regex]::Match($source, "(?m)^\s*($column\s+[^\r\n]+)\r?$")
    if (-not $match.Success) { throw "Missing maintained column: $column" }
    return $match.Groups[1].Value.Trim().TrimEnd(',')
}
function Read-CheckDefinition([string]$file, [string]$name) {
    $source = [IO.File]::ReadAllText((Join-Path $businessDirectory $file))
    $match = [regex]::Match($source, "(?m)^\s*(CONSTRAINT $name CHECK\s*\([^\r\n]+)\r?$")
    if (-not $match.Success) { throw "Missing maintained check: $name" }
    return $match.Groups[1].Value.Trim().TrimEnd(',')
}
$upgrade = @(
    '-- CF-01-UX-08: only for the 23-table MANAGEMENT-07 schema with connection_category.'
    '-- Verify backup and stop writers. sys_menu.navigation_group and import_item must be absent.'
    '-- This is a one-time incremental upgrade, not initialization or a rollback script.'
    'SET NAMES utf8mb4;'
    "SET time_zone = '+08:00';"
    'SET FOREIGN_KEY_CHECKS = 1;'
    ''
)
$menuColumn = Read-ColumnDefinition '菜单表.sql' 'navigation_group'
$menuCheck = Read-CheckDefinition '菜单表.sql' 'ck_menu_navigation_group'
$upgrade += "ALTER TABLE sys_menu ADD COLUMN $menuColumn AFTER sort_order, ADD $menuCheck;"
$clauses = @()
foreach ($column in @('job_kind', 'source_version', 'bulk_group_id', 'bulk_next_page', 'bulk_total',
        'bulk_processed_count', 'bulk_created_count', 'bulk_existing_count', 'bulk_failed_count',
        'bulk_duplicate_count', 'bulk_started_at')) {
    $clauses += 'ADD COLUMN ' + (Read-ColumnDefinition '相机接入任务表.sql' $column)
}
$clauses += 'MODIFY ' + (Read-ColumnDefinition '相机接入任务表.sql' 'secret_ciphertext')
foreach ($name in @('ck_access_job_kind', 'ck_access_job_bulk_counts')) {
    $clauses += 'ADD ' + (Read-CheckDefinition '相机接入任务表.sql' $name)
}
$upgrade += "ALTER TABLE camera_access_job`n " + ($clauses -join ",`n ") + ';'
$itemSource = [IO.File]::ReadAllText((Join-Path $businessDirectory '相机导入明细表.sql'))
$itemStart = [regex]::Match($itemSource, '(?m)^CREATE TABLE camera_access_import_item')
if (-not $itemStart.Success) { throw 'Missing maintained import item table.' }
$upgrade += $itemSource.Substring($itemStart.Index).Trim()
$upgrade += @'

-- Normalize only identified built-in navigation roots; labels and historical IDs may differ.
START TRANSACTION;
UPDATE camera_access_job SET job_kind='SCAN' WHERE method='SCAN';
UPDATE sys_menu root JOIN sys_menu page ON page.parent_id=root.id
 SET root.navigation_group='SYSTEM',root.sort_order=9000,root.version=root.version+1,root.updated_at=CURRENT_TIMESTAMP(6)
 WHERE root.parent_id IS NULL AND root.type='DIRECTORY' AND page.path='/system/menus' AND page.module_key='menu';
UPDATE sys_menu root JOIN sys_menu page ON page.parent_id=root.id
 SET root.navigation_group='MONITOR',root.sort_order=10000,root.version=root.version+1,root.updated_at=CURRENT_TIMESTAMP(6)
 WHERE root.parent_id IS NULL AND root.type='DIRECTORY' AND page.path='/monitor/Server' AND page.module_key='server';
UPDATE sys_menu root JOIN sys_menu page ON page.parent_id=root.id
 SET root.sort_order=0,root.version=root.version+1,root.updated_at=CURRENT_TIMESTAMP(6)
 WHERE root.parent_id IS NULL AND root.type='DIRECTORY' AND page.path='/camera/manage' AND page.module_key='camera';
-- Both pages already authorize the same camera module. Carry page-only role relations forward.
UPDATE sys_role role JOIN sys_role_menu relation ON relation.role_id=role.id
 JOIN sys_menu old_page ON old_page.id=relation.menu_id
 SET role.version=role.version+1,role.updated_at=CURRENT_TIMESTAMP(6)
 WHERE old_page.path='/camera/sources' AND old_page.component_key='/camera-source/Manage' AND old_page.module_key='camera';
INSERT INTO sys_role_menu (role_id,menu_id,created_at,created_by)
 SELECT old_rel.role_id,current_page.id,CURRENT_TIMESTAMP(6),NULL
 FROM sys_role_menu old_rel JOIN sys_menu old_page ON old_page.id=old_rel.menu_id
 JOIN sys_menu current_page ON current_page.path='/camera/manage' AND current_page.module_key='camera'
 LEFT JOIN sys_role_menu existing ON existing.role_id=old_rel.role_id AND existing.menu_id=current_page.id
 WHERE old_page.path='/camera/sources' AND old_page.component_key='/camera-source/Manage'
 AND old_page.module_key='camera' AND existing.role_id IS NULL;
DELETE old_rel FROM sys_role_menu old_rel JOIN sys_menu old_page ON old_page.id=old_rel.menu_id
 WHERE old_page.path='/camera/sources' AND old_page.component_key='/camera-source/Manage' AND old_page.module_key='camera';
DELETE FROM sys_menu WHERE path='/camera/sources' AND component_key='/camera-source/Manage' AND module_key='camera';
COMMIT;
'@
$upgradeText = (($upgrade -join "`n").TrimEnd() + "`n").Replace("`r`n", "`n")
$upgradeDirectory = Join-Path $sqlDirectory '升级'
$upgradeTarget = Join-Path $upgradeDirectory '20261006-cf01-ux.sql'
if ($Check) {
    if (-not (Test-Path -LiteralPath $upgradeTarget) -or
        [IO.File]::ReadAllText($upgradeTarget).Replace("`r`n", "`n") -cne $upgradeText) {
        throw 'CF-01 UX incremental SQL is stale. Run scripts/export-sql.ps1.'
    }
} else {
    [IO.Directory]::CreateDirectory($upgradeDirectory) | Out-Null
    [IO.File]::WriteAllText($upgradeTarget, $upgradeText, [Text.UTF8Encoding]::new($false))
}
Write-Output "SQL aggregate and current upgrade checked/generated from $($names.Count) table sources."
