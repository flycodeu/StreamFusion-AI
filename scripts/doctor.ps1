param(
    [string]$JavaHome,
    [string]$NodePath
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$failures = 0

function Report([string]$Name, [bool]$Ok, [string]$Detail) {
    if (!$Ok) { $script:failures++ }
    $status = if ($Ok) { 'PASS' } else { 'FAIL' }
    Write-Output "$status [$Name] $Detail"
}

# Only inspect the selected tools. Never install, change global settings or kill processes.
$javaCommand = Get-Command java -ErrorAction SilentlyContinue
$selectedJavaHome = if ($JavaHome) { $JavaHome } else { $env:JAVA_HOME }
$java = if ($selectedJavaHome) { Join-Path $selectedJavaHome 'bin/java.exe' } elseif ($javaCommand) { $javaCommand.Source } else { '' }
if ($java -and (Test-Path $java)) {
    $version = (& $java -version 2>&1 | Out-String).Trim()
    Report 'JDK' ($version -match 'version "21\.') "$java; $($version.Split([Environment]::NewLine)[0])"
} else { Report 'JDK' $false 'JDK 21 not found; set JAVA_HOME or pass -JavaHome.' }

$nodeCommand = Get-Command node -ErrorAction SilentlyContinue
$localNode = Join-Path $projectRoot '.tools/node.exe'
$node = if ($NodePath) { $NodePath } elseif (Test-Path $localNode) { $localNode } elseif ($nodeCommand) { $nodeCommand.Source } else { '' }
if ($node -and (Test-Path $node)) {
    $nodeVersion = & $node --version
    Report 'Node' ($nodeVersion -eq 'v24.21.0') "$node; $nodeVersion (expected v24.21.0)"
} else { Report 'Node' $false 'Node 24.21.0 not found.' }

Push-Location (Join-Path $projectRoot 'platform-web')
try {
    $pnpmCommand = Get-Command pnpm -ErrorAction SilentlyContinue
    if ($pnpmCommand) {
        $previousNetwork = $env:COREPACK_ENABLE_NETWORK
        try {
            $env:COREPACK_ENABLE_NETWORK = '0'
            $corepack = Get-Command corepack -ErrorAction SilentlyContinue
            $entry = if ($corepack) { Join-Path (Split-Path $corepack.Source) 'node_modules/corepack/dist/pnpm.js' } else { '' }
            if (($NodePath -or (Test-Path $localNode)) -and $entry -and (Test-Path $entry) -and (Test-Path $node)) {
                $pnpmVersion = (& $node $entry --version 2>&1 | Out-String).Trim()
                Report 'pnpm' ($LASTEXITCODE -eq 0 -and $pnpmVersion -eq '10.34.5') "node=$node; entry=$entry; version=$pnpmVersion (network disabled)"
            } else {
                $pnpmVersion = (& $pnpmCommand.Source --version 2>&1 | Out-String).Trim()
                Report 'pnpm' ($LASTEXITCODE -eq 0 -and $pnpmVersion -eq '10.34.5') "launcher=$($pnpmCommand.Source); version=$pnpmVersion (network disabled)"
                $launcherNode = Join-Path (Split-Path $pnpmCommand.Source) 'node.exe'
                if (Test-Path $launcherNode) {
                    $launcherVersion = & $launcherNode --version
                    Report 'pnpm launcher Node' ($launcherVersion -eq 'v24.21.0') "$launcherNode; $launcherVersion"
                }
                if ($NodePath) { Write-Output 'WARN [pnpm] Explicit NodePath was not used by this launcher; align the launcher before starting.' }
            }
        } finally { $env:COREPACK_ENABLE_NETWORK = $previousNetwork }
    } else { Report 'pnpm' $false 'pnpm launcher not found.' }
    Report 'Web dependencies' (Test-Path node_modules/.modules.yaml) 'Use pnpm install --frozen-lockfile if missing.'
    if (Test-Path .env.local) { Write-Output 'PASS [Web config] .env.local exists (contents not printed).' }
    else { Write-Output 'INFO [Web config] .env.local absent; default API target will be used.' }
} finally { Pop-Location }

Report 'Maven Wrapper' (Test-Path (Join-Path $projectRoot 'platform-api/.mvn/wrapper/maven-wrapper.properties')) 'Wrapper configuration present; no Maven download or build performed.'
$uvCommand = Get-Command uv -ErrorAction SilentlyContinue
Report 'uv' ([bool]$uvCommand) 'uv launcher required for locked environment installation.'

foreach ($project in @('node-agent', 'runtime')) {
    $directory = Join-Path $projectRoot "algorithm-node/$project"
    $python = Join-Path $directory '.venv/Scripts/python.exe'
    if (!(Test-Path $python)) { Report $project $false 'Missing .venv; run uv sync --locked in this subproject.'; continue }
    Push-Location $directory
    try {
        $result = & $python -B -m app.diagnostics 2>&1
        if ($LASTEXITCODE -ne 0) {
            Report $project $false 'Dependency import or settings validation failed; inspect using python -m app.diagnostics.'
        } else {
            $report = ($result | Out-String) | ConvertFrom-Json
            Report $project ($report.python -like '3.12.*') "python=$($report.python); executable=$($report.executable); fastapi=$($report.fastapi); settings=$($report.settings); port=$($report.port)"
        }
    } finally { Pop-Location }
}
if (Test-Path (Join-Path $projectRoot 'algorithm-node/.venv')) {
    Write-Output 'WARN [PyCharm] Parent algorithm-node/.venv exists. Select each subproject .venv, not this parent environment.'
}
foreach ($port in @(8080, 5173, 8100, 8101)) {
    $listeners = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue)
    if ($listeners.Count -eq 0) { Write-Output "PASS [Port $port] available" }
    else {
        $owners = $listeners.OwningProcess | Sort-Object -Unique
        Write-Output "WARN [Port $port] occupied by PID $($owners -join ', '); no process was stopped."
    }
}
Write-Output "Environment check finished: $failures failure(s)."
if ($failures -gt 0) { exit 1 }
