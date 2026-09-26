param(
    [ValidateSet('api', 'web', 'agent', 'runtime')]
    [ValidateNotNullOrEmpty()]
    [string[]]$Services = @('api', 'web', 'agent', 'runtime')
)

$ErrorActionPreference = 'Stop'
$checks = @(
    @{ Key = 'api'; Name = 'Platform API'; Url = 'http://127.0.0.1:8080/actuator/health' },
    @{ Key = 'web'; Name = 'Web -> API'; Url = 'http://127.0.0.1:8090/api/actuator/health' },
    @{ Key = 'agent'; Name = 'Node Agent'; Url = 'http://127.0.0.1:8100/health'; Service = 'node-agent' },
    @{ Key = 'runtime'; Name = 'Runtime'; Url = 'http://127.0.0.1:8101/health'; Service = 'algorithm-runtime' }
)
foreach ($check in $checks) {
    if ($check.Key -notin $Services) { continue }
    $result = Invoke-RestMethod -Uri $check.Url -TimeoutSec 10
    if ($result.status -ne 'UP') { throw "$($check.Name): expected UP" }
    if ($check.Service -and ($result.service -ne $check.Service -or $result.mode -ne 'skeleton')) {
        throw "$($check.Name): unexpected skeleton response"
    }
    Write-Output "PASS $($check.Name) $($check.Url)"
}
if ('web' -in $Services) {
    $page = Invoke-WebRequest 'http://127.0.0.1:8090/' -TimeoutSec 10 -UseBasicParsing
    if ($page.Content -notmatch '<title>StreamFusion AI</title>') { throw 'Unexpected web page' }
    Write-Output 'PASS frontend HTML (browser rendering requires separate verification)'
}
