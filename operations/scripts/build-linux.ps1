$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$projectRoot = Split-Path $PSScriptRoot -Parent
$previousEnvironment = @{}
foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
  $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
function Check-Command {
  param([string]$Step)
  if ($LASTEXITCODE -ne 0) { throw "$Step 失败，退出码 $LASTEXITCODE" }
}
Push-Location $projectRoot
try {
  pnpm --dir web install --frozen-lockfile
  Check-Command '依赖安装'
  pnpm --dir web build
  Check-Command '网页构建'
  node scripts/embed-web.mjs
  Check-Command '网页嵌入'
  go test ./...
  Check-Command 'Go测试'
  go vet ./...
  Check-Command 'Go静态检查'
  $env:GOOS = 'linux'
  $env:GOARCH = 'amd64'
  $env:CGO_ENABLED = '0'
  $output = Join-Path $projectRoot 'release/0.1.0'
  New-Item -ItemType Directory -Path $output -Force | Out-Null
  go build -trimpath -ldflags '-s -w' -o "$output/streamfusion-ops" ./cmd/streamfusion-ops
  Check-Command 'Linux交叉构建'
  Copy-Item scripts/install-linux.sh, scripts/uninstall-linux.sh, README.md -Destination $output -Force
  $files = @('streamfusion-ops', 'install-linux.sh', 'uninstall-linux.sh', 'README.md')
  $checksums = foreach ($file in $files) {
    $hash = (Get-FileHash -LiteralPath (Join-Path $output $file) -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $file"
  }
  [IO.File]::WriteAllText((Join-Path $output 'SHA256SUMS'), ($checksums -join "`n") + "`n", [Text.UTF8Encoding]::new($false))
  $archive = Join-Path $projectRoot 'release/streamfusion-ops-0.1.0-linux-amd64.tar.gz'
  tar -czf $archive -C $output streamfusion-ops SHA256SUMS install-linux.sh uninstall-linux.sh README.md
  Check-Command '发行包'
  Write-Output "Linux发行包: $archive"
} finally {
  foreach ($name in $previousEnvironment.Keys) {
    [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], 'Process')
  }
  Pop-Location
}
