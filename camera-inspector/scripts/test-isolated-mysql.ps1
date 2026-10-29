param(
    [string]$MysqldPath,
    [string]$PythonPath,
    [ValidateRange(0, 65535)][int]$Port = 0
)

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$workspaceRoot = [IO.Path]::GetFullPath((Join-Path $projectRoot '..'))
if (-not $MysqldPath) { $MysqldPath = (Get-Command mysqld.exe -ErrorAction Stop).Source }
if (-not $PythonPath) { $PythonPath = Join-Path $projectRoot '.venv/Scripts/python.exe' }
$MysqldPath = (Get-Item -LiteralPath $MysqldPath -ErrorAction Stop).FullName
$PythonPath = (Get-Item -LiteralPath $PythonPath -ErrorAction Stop).FullName
if ($Port -ne 0 -and $Port -lt 49152) { throw 'Only an unused high test port is allowed.' }

$requestedPort = $Port
$portAvailable = $false
for ($attempt = 0; $attempt -lt 20; $attempt++) {
    if ($requestedPort -eq 0) { $Port = Get-Random -Minimum 49152 -Maximum 65536 }
    $portProbe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $Port)
    try {
        $portProbe.Start()
        $portAvailable = $true
        break
    } catch {
        if ($requestedPort -ne 0) { throw 'The selected test port is not available.' }
    } finally {
        $portProbe.Stop()
    }
}
if (-not $portAvailable) { throw 'No unused high test port was found within 20 attempts.' }

$runRoot = Join-Path $workspaceRoot ('.run/inspection-p0/mysql-' + [Guid]::NewGuid().ToString('N'))
$dataDirectory = Join-Path $runRoot 'data'
$tempDirectory = Join-Path $runRoot 'tmp'
if (Test-Path -LiteralPath $runRoot) { throw 'The test directory must be new.' }
[IO.Directory]::CreateDirectory($dataDirectory) | Out-Null
[IO.Directory]::CreateDirectory($tempDirectory) | Out-Null
$mysqlProcess = $null
$initializeProcess = $null
$pytestProcess = $null
$shutdownProcess = $null
$enabledNames = @('SF_INSPECTOR_MYSQL_TEST', 'SF_INSPECTOR_TEST_DB_PORT',
    'SF_INSPECTOR_TEST_DB_TOKEN', 'SF_INSPECTOR_TEST_DB_MARKER')
$previousEnvironment = @{}
foreach ($name in $enabledNames) { $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }

try {
    $initializeArguments = @('--no-defaults', '--no-monitor', '--initialize-insecure',
        ('--datadir="' + $dataDirectory + '"'),
        ('--log-error="' + (Join-Path $runRoot 'initialize.log') + '"'))
    $initializeProcess = Start-Process -FilePath $MysqldPath -ArgumentList $initializeArguments `
        -WorkingDirectory $runRoot -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $runRoot 'initialize.stdout.log') `
        -RedirectStandardError (Join-Path $runRoot 'initialize.stderr.log')
    if (-not $initializeProcess.WaitForExit(60000)) { throw 'Isolated MySQL initialization timed out.' }
    if ($initializeProcess.ExitCode -ne 0) { throw 'Isolated MySQL initialization failed; inspect the run logs.' }

    $serverArguments = @('--no-defaults', '--no-monitor', ('--datadir="' + $dataDirectory + '"'),
        '--bind-address=127.0.0.1', ('--port=' + $Port), '--mysqlx=OFF', '--skip-log-bin',
        '--local-infile=0', '--innodb-buffer-pool-size=64M',
        ('--tmpdir="' + $tempDirectory + '"'), ('--secure-file-priv="' + $tempDirectory + '"'),
        ('--pid-file="' + (Join-Path $runRoot 'mysql.pid') + '"'),
        ('--log-error="' + (Join-Path $runRoot 'mysql.log') + '"'))
    $mysqlProcess = Start-Process -FilePath $MysqldPath -ArgumentList $serverArguments `
        -WorkingDirectory $runRoot -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $runRoot 'mysql.stdout.log') `
        -RedirectStandardError (Join-Path $runRoot 'mysql.stderr.log')
    $startupDeadline = [DateTime]::UtcNow.AddSeconds(45)
    $ready = $false
    while ([DateTime]::UtcNow -lt $startupDeadline) {
        if ($mysqlProcess.HasExited) { throw 'Isolated MySQL exited before accepting connections.' }
        $listener = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
        if ($listener.Count -gt 0) {
            if (@($listener | Where-Object { $_.OwningProcess -ne $mysqlProcess.Id }).Count -gt 0) {
                throw 'The test port is owned by another process; refusing to connect.'
            }
            $ready = $true
            break
        }
        Start-Sleep -Milliseconds 200
    }
    if (-not $ready) { throw 'Isolated MySQL did not become ready within 45 seconds.' }

    $testToken = [Guid]::NewGuid().ToString('N')
    $markerPath = Join-Path $runRoot 'instance.json'
    $marker = @{ token = $testToken; port = $Port; datadir = $dataDirectory; pid = $mysqlProcess.Id }
    [IO.File]::WriteAllText($markerPath, ($marker | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
    [Environment]::SetEnvironmentVariable('SF_INSPECTOR_MYSQL_TEST', '1', 'Process')
    [Environment]::SetEnvironmentVariable('SF_INSPECTOR_TEST_DB_PORT', [string]$Port, 'Process')
    [Environment]::SetEnvironmentVariable('SF_INSPECTOR_TEST_DB_TOKEN', $testToken, 'Process')
    [Environment]::SetEnvironmentVariable('SF_INSPECTOR_TEST_DB_MARKER', $markerPath, 'Process')

    $reportPath = Join-Path $runRoot 'pytest-mysql.xml'
    Write-Output ('Isolated MySQL test instance: 127.0.0.1:' + $Port + '; PID ' + $mysqlProcess.Id)
    Write-Output ('Evidence directory: ' + $runRoot)
    $pytestArguments = @('-m', 'pytest', '-m', 'mysql', 'tests', '--junitxml',
        ('"' + $reportPath + '"'), '-q')
    $pytestProcess = Start-Process -FilePath $PythonPath -ArgumentList $pytestArguments `
        -WorkingDirectory $projectRoot -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $runRoot 'pytest-mysql.log') `
        -RedirectStandardError (Join-Path $runRoot 'pytest-mysql.stderr.log')
    if (-not $pytestProcess.WaitForExit(300000)) { throw 'MySQL contract tests exceeded five minutes.' }
    Get-Content -LiteralPath (Join-Path $runRoot 'pytest-mysql.log')
    if ($pytestProcess.ExitCode -ne 0) {
        throw ('MySQL contract tests failed with exit code ' + $pytestProcess.ExitCode)
    }
    [xml]$report = [IO.File]::ReadAllText($reportPath)
    $cases = @($report.SelectNodes('//testcase'))
    $skipped = @($report.SelectNodes('//testcase/skipped'))
    if ($cases.Count -eq 0 -or $skipped.Count -gt 0) { throw 'The isolated run requires tests to execute without skips.' }
    Write-Output ('MySQL verification passed: ' + $cases.Count + ' executed, zero skipped.')
} finally {
    foreach ($name in $enabledNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], 'Process')
    }
    $cleanupErrors = @()
    foreach ($ownedProcess in @($initializeProcess, $pytestProcess)) {
        try {
            if ($ownedProcess -and -not $ownedProcess.HasExited) {
                $ownedProcess.Kill()
                if (-not $ownedProcess.WaitForExit(5000)) { throw 'An owned child process did not exit.' }
            }
        } catch { $cleanupErrors += $_.Exception.Message }
    }
    try {
        if ($mysqlProcess -and -not $mysqlProcess.HasExited) {
            $ownedListener = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
            if ($ownedListener.Count -gt 0 -and
                @($ownedListener | Where-Object { $_.OwningProcess -ne $mysqlProcess.Id }).Count -eq 0) {
                # Use an explicit driver connection, never a client login-path or machine defaults.
                $shutdownCode = "import pathlib,sys,pymysql; c=pymysql.connect(host='127.0.0.1',port=int(sys.argv[1]),user='root',password='',connect_timeout=3,read_timeout=3,write_timeout=3); q=c.cursor(); q.execute('SELECT @@port,@@datadir'); p,d=q.fetchone(); assert p==int(sys.argv[1]) and pathlib.Path(d).resolve()==pathlib.Path(sys.argv[2]).resolve(); q.execute('SHUTDOWN'); c.close()"
                $shutdownArguments = @('-c', ('"' + $shutdownCode + '"'), [string]$Port,
                    ('"' + $dataDirectory + '"'))
                $shutdownProcess = Start-Process -FilePath $PythonPath -ArgumentList $shutdownArguments `
                    -WorkingDirectory $runRoot -WindowStyle Hidden -PassThru `
                    -RedirectStandardOutput (Join-Path $runRoot 'shutdown.log') `
                    -RedirectStandardError (Join-Path $runRoot 'shutdown.stderr.log')
                if (-not $shutdownProcess.WaitForExit(5000)) {
                    $shutdownProcess.Kill()
                    if (-not $shutdownProcess.WaitForExit(5000)) {
                        throw 'The owned shutdown helper did not exit.'
                    }
                }
            }
            $null = $mysqlProcess.WaitForExit(15000)
        }
    } catch { $cleanupErrors += $_.Exception.Message }
    finally {
        try {
            if ($mysqlProcess -and -not $mysqlProcess.HasExited) {
                $mysqlProcess.Kill()
                if (-not $mysqlProcess.WaitForExit(5000)) { throw 'The owned isolated MySQL process did not exit.' }
            }
        } catch { $cleanupErrors += $_.Exception.Message }
    }
    if ($cleanupErrors.Count -gt 0) { throw ($cleanupErrors -join ' ') }
    if ($mysqlProcess -and $mysqlProcess.HasExited) {
        Write-Output ('Owned isolated MySQL PID ' + $mysqlProcess.Id + ' exited; evidence retained.')
    }
}
