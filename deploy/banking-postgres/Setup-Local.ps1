[CmdletBinding()]
param(
    [string] $PostgresContainer = 'qingrongjin-agent-banking-agent-postgres-1',
    [switch] $ConfigureOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$deploymentRoot = [IO.Path]::GetFullPath($PSScriptRoot)
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $deploymentRoot '..\..'))
$utf8 = [Text.UTF8Encoding]::new($false)
$credentialPath = Join-Path $deploymentRoot 'credentials.env.local'
$hostSource = Join-Path $repositoryRoot 'deploy\banking\.env.local'
$composeSource = Join-Path $repositoryRoot 'deploy\banking\.env'

function Read-Environment {
    param([string] $Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "Required private configuration does not exist: $Path" }
    $values = [ordered]@{}
    foreach ($line in [IO.File]::ReadAllLines($Path)) {
        $trimmed = $line.Trim()
        if ($trimmed.StartsWith('#') -or -not $trimmed.Contains('=')) { continue }
        $separator = $trimmed.IndexOf('=')
        $name = $trimmed.Substring(0, $separator).Trim()
        if ($name -notmatch '^[A-Z][A-Z0-9_]*$') { throw "Unsupported environment key in $Path" }
        # Preserve the exact value syntax of the existing Banking env file.
        $values[$name] = $trimmed.Substring($separator + 1)
    }
    return $values
}

function New-IndependentPassword {
    $bytes = [byte[]]::new(32)
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $random.GetBytes($bytes); return ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant() }
    finally { $random.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
}

function Protect-PrivateFile {
    param([string] $Path)
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { return }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User
    # Update only DACL permissions. Changing owner/audit data can require
    # SeSecurityPrivilege on a subsequent non-elevated invocation.
    $acl = [IO.File]::GetAccessControl($Path, [Security.AccessControl.AccessControlSections]::Access)
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($existing in @($acl.Access)) { [void]$acl.RemoveAccessRuleAll($existing) }
    foreach ($principal in @($identity, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'))) {
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($principal, 'FullControl', 'Allow'))
    }
    [IO.File]::SetAccessControl($Path, $acl)
}

function Write-PrivateEnvironment {
    param([string] $Path, [System.Collections.IDictionary] $Values, [switch] $CreateNew)
    $mode = if ($CreateNew) { [IO.FileMode]::CreateNew } else { [IO.FileMode]::Create }
    $stream = [IO.File]::Open($Path, $mode, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $lines = @('# Generated private P7 local configuration. Never commit or print.')
        foreach ($name in $Values.Keys) { $lines += "$name=$($Values[$name])" }
        $content = $utf8.GetBytes(($lines -join "`n") + "`n")
        $stream.Write($content, 0, $content.Length)
        [Array]::Clear($content, 0, $content.Length)
    } finally { $stream.Dispose() }
    Protect-PrivateFile -Path $Path
}

function Copy-Environment {
    param([System.Collections.IDictionary] $Source)
    $result = [ordered]@{}
    foreach ($key in $Source.Keys) { $result[$key] = $Source[$key] }
    return $result
}

function Set-PostgresEnvironment {
    param([System.Collections.IDictionary] $Values, [string] $User, [string] $Password, [string] $Database, [string] $HostName, [int] $Port)
    $Values['BANKING_DATABASE_DRIVER'] = 'postgres'
    $Values['BANKING_POSTGRES_HOST'] = $HostName
    $Values['BANKING_POSTGRES_PORT'] = [string]$Port
    $Values['BANKING_POSTGRES_USER'] = $User
    $Values['BANKING_POSTGRES_PASSWORD'] = $Password
    $Values['BANKING_POSTGRES_DATABASE'] = $Database
    # Local loopback/private Docker network only. Production requires verified TLS.
    $Values['BANKING_POSTGRES_SSLMODE'] = 'disable'
}

function Invoke-PrivateSQL {
    param([string] $Database, [string] $SQL)
    if ($PostgresContainer -notmatch '^[a-zA-Z0-9_.-]+$') { throw 'Invalid PostgreSQL container name.' }
    if ($Database -notmatch '^[a-z_][a-z0-9_]*$') { throw 'Invalid database name.' }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = [Diagnostics.ProcessStartInfo]::new()
    $process.StartInfo.FileName = 'docker'
    # Passwords and SQL travel only over stdin. Docker arguments contain no credentials.
    $shellCommand = 'PGPASSWORD="$POSTGRES_PASSWORD" exec psql --no-psqlrc --host=127.0.0.1 --username=postgres --dbname=' + $Database + ' --quiet --tuples-only --no-align --set=ON_ERROR_STOP=1'
    $arguments = @('exec', '-i', $PostgresContainer, 'sh', '-c', $shellCommand)
    $quotedArguments = foreach ($argument in $arguments) {
        # Windows CommandLineToArgvW escaping; quote characters are data for sh.
        '"' + (($argument -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
    }
    $process.StartInfo.Arguments = $quotedArguments -join ' '
    $process.StartInfo.UseShellExecute = $false
    $process.StartInfo.CreateNoWindow = $true
    $process.StartInfo.RedirectStandardInput = $true
    $process.StartInfo.RedirectStandardOutput = $true
    $process.StartInfo.RedirectStandardError = $true
    try {
        [void]$process.Start()
        $outputTask = $process.StandardOutput.ReadToEndAsync()
        $errorTask = $process.StandardError.ReadToEndAsync()
        $process.StandardInput.Write($SQL)
        $process.StandardInput.Close()
        if (-not $process.WaitForExit(30000)) { $process.Kill(); throw 'PostgreSQL provisioning exceeded 30 seconds; configuration files were preserved.' }
        $output = $outputTask.GetAwaiter().GetResult()
        [void]$errorTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) { throw 'PostgreSQL provisioning failed. Raw SQL errors are intentionally withheld because they may contain credentials; existing data was not removed.' }
        return $output.Trim()
    } finally { $process.Dispose() }
}

$hostEnvironment = Read-Environment -Path $hostSource
$composeEnvironment = Read-Environment -Path $composeSource
if (-not (Test-Path -LiteralPath $credentialPath)) {
    Write-PrivateEnvironment -Path $credentialPath -CreateNew -Values ([ordered]@{
        BANKING_FINANCE_MIGRATOR_PASSWORD = New-IndependentPassword
        BANKING_FINANCE_APP_PASSWORD = New-IndependentPassword
    })
}
$credentials = Read-Environment -Path $credentialPath
$migratorPassword = $credentials['BANKING_FINANCE_MIGRATOR_PASSWORD']
$appPassword = $credentials['BANKING_FINANCE_APP_PASSWORD']
if ($migratorPassword -notmatch '^[a-f0-9]{64}$' -or $appPassword -notmatch '^[a-f0-9]{64}$' -or $migratorPassword -eq $appPassword) {
    throw 'Existing P7 credentials must contain two separate generated 256-bit passwords; no credentials were replaced.'
}

$files = [ordered]@{}
foreach ($specification in @(
    @('finance.migrate.env.local', 'banking_finance_migrator', $migratorPassword, 'qingrongjin_finance'),
    @('finance.test.env.local', 'banking_finance_migrator', $migratorPassword, 'qingrongjin_finance_test'),
    @('finance.core-test.env.local', 'banking_finance_migrator', $migratorPassword, 'qingrongjin_finance_core_test'),
    @('finance.acceptance-test.env.local', 'banking_finance_migrator', $migratorPassword, 'qingrongjin_finance_acceptance_test'),
    @('finance.app.env.local', 'banking_finance_app', $appPassword, 'qingrongjin_finance')
)) {
    $values = Copy-Environment -Source $hostEnvironment
    Set-PostgresEnvironment -Values $values -User $specification[1] -Password $specification[2] -Database $specification[3] -HostName '127.0.0.1' -Port 15432
    $values['BANKING_MYSQL_PASSWORD'] = ''
    $files[$specification[0]] = $values
}
$values = Copy-Environment -Source $composeEnvironment
Set-PostgresEnvironment -Values $values -User 'banking_finance_app' -Password $appPassword -Database 'qingrongjin_finance' -HostName 'banking-agent-postgres' -Port 5432
$values['BANKING_FINANCE_MIGRATOR_PASSWORD'] = $migratorPassword
$values['BANKING_IMAGE'] = 'qingyingjin-banking-service:p7'
# Base Compose still needs the old MySQL variables to parse; the P7 overlay clears
# the runtime MySQL password and has no MySQL readiness dependency.
$files['finance.compose.env.local'] = $values
foreach ($name in $files.Keys) { Write-PrivateEnvironment -Path (Join-Path $deploymentRoot $name) -Values $files[$name] }

if (-not $ConfigureOnly) {
    $rolesSQL = @"
\set ECHO none
SELECT 'CREATE ROLE banking_finance_migrator' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='banking_finance_migrator') \gexec
SELECT 'CREATE ROLE banking_finance_app' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='banking_finance_app') \gexec
ALTER ROLE banking_finance_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '$migratorPassword';
ALTER ROLE banking_finance_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '$appPassword';
SELECT 'CREATE DATABASE qingrongjin_finance OWNER banking_finance_migrator' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='qingrongjin_finance') \gexec
SELECT 'CREATE DATABASE qingrongjin_finance_test OWNER banking_finance_migrator' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='qingrongjin_finance_test') \gexec
SELECT 'CREATE DATABASE qingrongjin_finance_core_test OWNER banking_finance_migrator' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='qingrongjin_finance_core_test') \gexec
SELECT 'CREATE DATABASE qingrongjin_finance_acceptance_test OWNER banking_finance_migrator' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='qingrongjin_finance_acceptance_test') \gexec
"@
    [void](Invoke-PrivateSQL -Database 'postgres' -SQL $rolesSQL)
    foreach ($database in @('qingrongjin_finance', 'qingrongjin_finance_test', 'qingrongjin_finance_core_test', 'qingrongjin_finance_acceptance_test')) {
        # Core regression tests use the owner role and must never contaminate the
        # pristine import-validation database or grant the app extra access.
        $appPrivilegesSQL = if ($database -in @('qingrongjin_finance_core_test', 'qingrongjin_finance_acceptance_test')) { '' } else { @"
GRANT CONNECT ON DATABASE $database TO banking_finance_app;
GRANT USAGE ON SCHEMA public TO banking_finance_app;
ALTER DEFAULT PRIVILEGES FOR ROLE banking_finance_migrator IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO banking_finance_app;
ALTER DEFAULT PRIVILEGES FOR ROLE banking_finance_migrator IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO banking_finance_app;
"@ }
        $accessSQL = @"
\set ECHO none
DO `$guard`$ BEGIN
IF (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()) <> 'banking_finance_migrator' THEN RAISE EXCEPTION 'Existing database has unexpected owner'; END IF;
END `$guard`$;
REVOKE ALL ON DATABASE $database FROM PUBLIC;
ALTER SCHEMA public OWNER TO banking_finance_migrator;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM banking_finance_app;
$appPrivilegesSQL
SELECT current_database() || ': provisioned';
"@
        [void](Invoke-PrivateSQL -Database $database -SQL $accessSQL)
    }
}

[ordered]@{
    configured = $true
    provisioned = -not [bool]$ConfigureOnly
    databases = @('qingrongjin_finance', 'qingrongjin_finance_test', 'qingrongjin_finance_core_test', 'qingrongjin_finance_acceptance_test')
    migration_role = 'banking_finance_migrator'
    runtime_role = 'banking_finance_app'
    files = @($files.Keys)
    banking_service_restarted = $false
    business_authority_switched = $false
} | ConvertTo-Json -Depth 3 -Compress
