# Starts studio.exe from $env:BIN in the background, for the upgrade job of the smoke workflow,
# and waits until it serves. The runner runs steps in a job object that doesn't allow
# breakaway, and msb starts each VM detached with CREATE_BREAKAWAY_FROM_JOB, which then fails
# with "Access is denied". WMI starts Studio outside the job, as a user's own start would.
$ErrorActionPreference = 'Stop'
$bin = [IO.Path]::GetFullPath($env:BIN)
$exe = Join-Path $bin studio.exe
$log = Join-Path $env:RUNNER_TEMP studio.log
$cmd = "cmd.exe /c set SANDBOX_STUDIO_HOME=$env:SANDBOX_STUDIO_HOME&& `"$exe`" >> `"$log`" 2>&1"
$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = $cmd; CurrentDirectory = $bin }
if ($r.ReturnValue -ne 0) { throw "Win32_Process.Create returned $($r.ReturnValue)" }
# The auth status answers once Studio listens, without a session.
for ($i = 0; $i -lt 600; $i++) {
  try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 http://127.0.0.1:7878/api/auth/status | Out-Null; exit 0 } catch { }
  Start-Sleep 1
}
Get-Content $log -ErrorAction SilentlyContinue
Get-Content (Join-Path $env:SANDBOX_STUDIO_HOME logs\studio.log) -ErrorAction SilentlyContinue
exit 1
