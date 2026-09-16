param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('api', 'web', 'agent', 'runtime')]
    [string]$Project,
    [string]$JavaHome,
    [ValidateSet('dev', 'local', 'prod')]
    [string]$Profile
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$originalPath = $env:Path
$originalJavaHome = $env:JAVA_HOME
Push-Location $projectRoot
try {
    switch ($Project) {
        'api' {
            if ($JavaHome) { $env:JAVA_HOME = $JavaHome }
            if ($env:JAVA_HOME) { $env:Path = "$env:JAVA_HOME\bin;$env:Path" }
            $javaInfo = (& java -version 2>&1 | Out-String)
            if ($javaInfo -notmatch 'version "21\.') { throw 'JDK 21 is required. Set JAVA_HOME or pass -JavaHome.' }
            Set-Location platform-api
            if ($Profile) {
                & .\mvnw.cmd spring-boot:run "-Dspring-boot.run.profiles=$Profile"
            } else {
                & .\mvnw.cmd spring-boot:run
            }
        }
        'web' {
            Set-Location platform-web
            $localNode = Join-Path $projectRoot '.tools\node.exe'
            if (Test-Path $localNode) {
                $env:Path = "$(Split-Path $localNode);$env:Path"
                $corepackCommand = Get-Command corepack -ErrorAction Stop
                $pnpmEntry = Join-Path (Split-Path $corepackCommand.Source) 'node_modules\corepack\dist\pnpm.js'
                if (!(Test-Path $pnpmEntry)) { throw 'Install Node 24 and pnpm 10.34.5; see README.' }
                & $localNode $pnpmEntry dev
            } else {
                if ((& node --version) -notmatch '^v24\.') { throw 'Node.js 24 is required; see README.' }
                & pnpm dev
            }
        }
        'agent' {
            Set-Location algorithm-node/node-agent
            & uv run --locked python -m uvicorn app.main:app --host 127.0.0.1 --port 8100
        }
        'runtime' {
            Set-Location algorithm-node/runtime
            & uv run --locked python -m uvicorn app.main:app --host 127.0.0.1 --port 8101
        }
    }
    if ($LASTEXITCODE -ne 0) { throw "$Project exited with code $LASTEXITCODE" }
} finally {
    Pop-Location
    $env:Path = $originalPath
    $env:JAVA_HOME = $originalJavaHome
}
