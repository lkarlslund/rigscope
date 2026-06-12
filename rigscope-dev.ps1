param(
    [string]$Addr = "127.0.0.1:7077",
    [string]$DataDir = "data",
    [string]$Interval = "1s",
    [string]$Retention = "168h",
    [string]$LogLevel = "info"
)

$ErrorActionPreference = "Stop"

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go -and (Test-Path "C:\Program Files\Go\bin\go.exe")) {
    $go = Get-Item "C:\Program Files\Go\bin\go.exe"
}
if (-not $go) {
    throw "go.exe was not found. Add C:\Program Files\Go\bin to PATH or install Go."
}

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$outDir = Join-Path $root ".tmp"
$exe = Join-Path $outDir "rigscope-dev.exe"
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

& $go.Source build -o $exe ./cmd/rigscope
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$args = @(
    "serve",
    "--addr", $Addr,
    "--data-dir", $DataDir,
    "--interval", $Interval,
    "--retention", $Retention,
    "--log-level", $LogLevel
)

$process = Start-Process -FilePath $exe -ArgumentList $args -WorkingDirectory $root -PassThru
$endpoint = "http://$Addr/api/build"
try {
    for ($i = 0; $i -lt 30; $i++) {
        try {
            $build = Invoke-RestMethod -Uri $endpoint -TimeoutSec 2
            Write-Host "rigscope listening at http://$Addr"
            Write-Host "version=$($build.version) commit=$($build.commit) pid=$($build.pid)"
            Wait-Process -Id $process.Id
            exit $process.ExitCode
        } catch {
            if ($process.HasExited) {
                throw "rigscope exited early with code $($process.ExitCode)"
            }
            Start-Sleep -Milliseconds 250
        }
    }
    throw "timed out waiting for $endpoint"
} finally {
    if (-not $process.HasExited) {
        Stop-Process -Id $process.Id
    }
}
