param(
    [Parameter(Mandatory = $true)][ValidateRange(1, 65535)][int]$Port,
    [Parameter(Mandatory = $true)][string]$ViteEntry,
    [Parameter(Mandatory = $true)][string]$ViteRealEntry
)

$ErrorActionPreference = 'Stop'
$owners = @(Get-NetTCPConnection -State Listen -ErrorAction Stop |
    Where-Object LocalPort -eq $Port | Select-Object -ExpandProperty OwningProcess -Unique)
$allowedEntries = @([IO.Path]::GetFullPath($ViteEntry), [IO.Path]::GetFullPath($ViteRealEntry))
$confirmed = @()
foreach ($owner in $owners) {
    $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $owner"
    if (!$processInfo) { continue }
    # Only an absolute Vite script path from this checkout proves ownership.
    $command = [regex]::Match($processInfo.CommandLine, '^\s*(?:"[^"]+"|\S+)\s+(?:"(?<entry>[^"]+)"|(?<entry>\S+))(?:\s|$)')
    $entry = $command.Groups['entry'].Value
    if ($processInfo.Name -ne 'node.exe' -or !$command.Success -or $allowedEntries -notcontains $entry) {
        throw "Port $Port belongs to PID $owner ($($processInfo.Name)), not a verified Vite instance from this project. Stop it manually or use pnpm dev --port <free-port>. No process was stopped."
    }
    $confirmed += $processInfo
}

foreach ($processInfo in $confirmed) {
    $current = Get-CimInstance Win32_Process -Filter "ProcessId = $($processInfo.ProcessId)"
    if (!$current) { continue }
    if ($current.CreationDate -ne $processInfo.CreationDate -or $current.CommandLine -ne $processInfo.CommandLine) {
        throw 'Port owner changed during inspection. Retry startup.'
    }
    Stop-Process -Id $current.ProcessId -ErrorAction Stop
    Write-Output "Stopped previous project Vite PID $($current.ProcessId) on port $Port."
}

if ($confirmed.Count) {
    $deadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
        $remaining = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object LocalPort -eq $Port)
        if (!$remaining.Count) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Port $Port was not released within 5 seconds. Check its current owner."
}
