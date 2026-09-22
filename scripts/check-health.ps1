$ErrorActionPreference = 'Stop'
$checks = @(
    @{ Name = 'Platform API'; Url = 'http://127.0.0.1:8080/actuator/health' },
    @{ Name = 'Web -> API'; Url = 'http://127.0.0.1:8090/actuator/health' },
    @{ Name = 'Node Agent'; Url = 'http://127.0.0.1:8100/health'; Service = 'node-agent' },
    @{ Name = 'Runtime'; Url = 'http://127.0.0.1:8101/health'; Service = 'algorithm-runtime' }
)
foreach ($check in $checks) {
    $result = Invoke-RestMethod -Uri $check.Url -TimeoutSec 10
    if ($result.status -ne 'UP') { throw "$($check.Name): expected UP" }
    if ($check.Service -and ($result.service -ne $check.Service -or $result.mode -ne 'skeleton')) {
        throw "$($check.Name): unexpected skeleton response"
    }
    Write-Output "PASS $($check.Name) $($check.Url)"
}
$page = Invoke-WebRequest 'http://127.0.0.1:8090/' -TimeoutSec 10
if ($page.Content -notmatch '<title>StreamFusion AI</title>') { throw 'Unexpected web page' }
Write-Output 'PASS frontend HTML (browser rendering requires separate verification)'
