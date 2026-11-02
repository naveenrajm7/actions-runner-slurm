$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$runnerSource = 'C:\actions-runner'
$runnerRoot = 'C:\runner\runner'
$diagnosticsDir = 'C:\runner-logs'
$claimConfig = 'C:\runner\.claim-curl.conf'
$claimPayload = 'C:\runner\.claim-request.json'
$jitFile = 'C:\runner\.jit-config'
$runnerStatus = 1

function Require-EnvironmentVariable([string] $Name) {
    $value = [Environment]::GetEnvironmentVariable($Name, 'Process')
    if ([string]::IsNullOrWhiteSpace($value)) {
        throw "missing $Name"
    }
    return $value
}

function Remove-RunnerEnvironment {
    foreach ($name in @(
        'SLURM_GHA_SERVICE_URL',
        'SLURM_GHA_LEASE_ID',
        'SLURM_GHA_CLASS',
        'SLURM_GHA_CLAIM_TOKEN',
        'SLURM_GHA_CA_FILE',
        'ACTIONS_RUNNER_INPUT_JITCONFIG'
    )) {
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }
}

try {
    $serviceUrl = Require-EnvironmentVariable 'SLURM_GHA_SERVICE_URL'
    $leaseId = Require-EnvironmentVariable 'SLURM_GHA_LEASE_ID'
    $runnerClass = Require-EnvironmentVariable 'SLURM_GHA_CLASS'
    $claimToken = Require-EnvironmentVariable 'SLURM_GHA_CLAIM_TOKEN'
    $jobId = Require-EnvironmentVariable 'SLURM_JOB_ID'

    $serviceUri = $null
    if (-not [Uri]::TryCreate($serviceUrl, [UriKind]::Absolute, [ref] $serviceUri) -or $serviceUri.Scheme -ne 'https') {
        throw 'SLURM_GHA_SERVICE_URL must be an absolute HTTPS URL'
    }
    if ($leaseId -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$') {
        throw 'invalid SLURM_GHA_LEASE_ID'
    }
    if ($runnerClass -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$') {
        throw 'invalid SLURM_GHA_CLASS'
    }
    if ($jobId -notmatch '^[0-9]+$') {
        throw 'invalid SLURM_JOB_ID'
    }
    if ($claimToken -match '[\r\n]') {
        throw 'invalid SLURM_GHA_CLAIM_TOKEN'
    }
    if (-not (Test-Path -LiteralPath $runnerSource -PathType Container)) {
        throw "runner distribution not found at $runnerSource"
    }

    New-Item -ItemType Directory -Force -Path $diagnosticsDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $runnerRoot) | Out-Null
    if (Test-Path -LiteralPath $runnerRoot) {
        Remove-Item -LiteralPath $runnerRoot -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $runnerRoot | Out-Null
    Get-ChildItem -LiteralPath $runnerSource -Force | Copy-Item -Destination $runnerRoot -Recurse -Force

    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    $escapedToken = $claimToken.Replace('\', '\\').Replace('"', '\"')
    [IO.File]::WriteAllText($claimConfig, "header = `"Authorization: Bearer $escapedToken`"`n", $utf8NoBom)
    [IO.File]::WriteAllText($claimPayload, "{`"jobId`":$jobId}`n", $utf8NoBom)
    [Environment]::SetEnvironmentVariable('SLURM_GHA_CLAIM_TOKEN', $null, 'Process')
    $claimToken = $null

    $claimUrl = $serviceUrl.TrimEnd('/') + '/api/v1/leases/' + $leaseId + '/claim'
    $curlArgs = @(
        '--config', $claimConfig,
        '--fail', '--silent', '--show-error',
        '--connect-timeout', '10', '--max-time', '60',
        '--request', 'POST',
        '--header', 'Content-Type: application/json',
        '--data-binary', "@$claimPayload",
        '--output', $jitFile
    )
    $caFile = [Environment]::GetEnvironmentVariable('SLURM_GHA_CA_FILE', 'Process')
    if (-not [string]::IsNullOrWhiteSpace($caFile)) {
        if (-not (Test-Path -LiteralPath $caFile -PathType Leaf)) {
            throw "callback CA not found at $caFile"
        }
        $curlArgs += @('--cacert', $caFile)
    }
    $curlArgs += $claimUrl

    $claimed = $false
    for ($attempt = 1; $attempt -le 12; $attempt++) {
        & curl.exe @curlArgs
        if ($LASTEXITCODE -eq 0) {
            $claimed = $true
            break
        }
        if ($attempt -lt 12) {
            Start-Sleep -Seconds ([Math]::Min($attempt, 5))
        }
    }
    if (-not $claimed) {
        throw 'runner claim failed after 12 attempts'
    }

    Remove-Item -LiteralPath $claimConfig, $claimPayload -Force -ErrorAction SilentlyContinue
    $jitConfig = [IO.File]::ReadAllText($jitFile)
    if ([string]::IsNullOrWhiteSpace($jitConfig)) {
        throw 'claim returned an empty JIT configuration'
    }
    [Environment]::SetEnvironmentVariable('ACTIONS_RUNNER_INPUT_JITCONFIG', $jitConfig, 'Process')
    $jitConfig = $null
    Remove-Item -LiteralPath $jitFile -Force
    foreach ($name in @('SLURM_GHA_SERVICE_URL', 'SLURM_GHA_LEASE_ID', 'SLURM_GHA_CLASS', 'SLURM_GHA_CA_FILE')) {
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }

    $commandProcessor = Join-Path $env:SystemRoot 'System32\cmd.exe'
    $runnerStartInfo = New-Object System.Diagnostics.ProcessStartInfo
    $runnerStartInfo.FileName = $commandProcessor
    $runnerStartInfo.Arguments = '/d /s /c run.cmd'
    $runnerStartInfo.WorkingDirectory = $runnerRoot
    $runnerStartInfo.UseShellExecute = $false
    $runnerProcess = New-Object System.Diagnostics.Process
    $runnerProcess.StartInfo = $runnerStartInfo
    if (-not $runnerProcess.Start()) {
        throw 'failed to start run.cmd'
    }
    [Environment]::SetEnvironmentVariable('ACTIONS_RUNNER_INPUT_JITCONFIG', $null, 'Process')
    $runnerProcess.WaitForExit()
    $runnerStatus = $runnerProcess.ExitCode
}
catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    $runnerStatus = 1
}
finally {
    Remove-Item -LiteralPath $claimConfig, $claimPayload, $jitFile -Force -ErrorAction SilentlyContinue
    Remove-RunnerEnvironment
    $diagSource = Join-Path $runnerRoot '_diag'
    if (Test-Path -LiteralPath $diagSource -PathType Container) {
        try {
            New-Item -ItemType Directory -Force -Path $diagnosticsDir | Out-Null
            Get-ChildItem -LiteralPath $diagSource -Force | Copy-Item -Destination $diagnosticsDir -Recurse -Force
        }
        catch {
            [Console]::Error.WriteLine("failed to copy runner diagnostics: $($_.Exception.Message)")
        }
    }
}

exit $runnerStatus
