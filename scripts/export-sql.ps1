param([switch]$Check)

$ErrorActionPreference = 'Stop'
$sqlDirectory = Join-Path $PSScriptRoot '../platform-api/sql'
$businessDirectory = Join-Path $sqlDirectory '业务'
$aggregateDirectory = Join-Path $sqlDirectory '汇总'
# Dependency order only; table definitions and seed data live in these files.
$names = @('部门表.sql', '用户表.sql', '角色表.sql', '权限表.sql', '菜单表.sql',
    '用户角色关联表.sql', '角色权限关联表.sql', '操作审计表.sql')
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
$created = @{}
foreach ($name in $names) {
    $sql = [IO.File]::ReadAllText((Join-Path $businessDirectory $name)).Replace("`r`n", "`n")
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
$sections += $drops
$sections += ''
$sections += $bodies
$expected = ($sections -join "`n").TrimEnd() + "`n"
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
Write-Output "SQL aggregate checked/generated from $($names.Count) table sources."
