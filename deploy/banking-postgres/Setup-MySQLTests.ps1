[CmdletBinding()]
param(
    [string] $MySQLContainer = 'qingyingjin-banking-mysql-1',
    [switch] $AdditiveOnly
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$sourcePath = Join-Path $repositoryRoot 'deploy\banking\.env.local'
$credentialPath = Join-Path $PSScriptRoot 'mysql-tests.credentials.env.local'
$utf8 = [Text.UTF8Encoding]::new($false)
if ($MySQLContainer -notmatch '^[a-zA-Z0-9_.-]+$') { throw 'Invalid MySQL container name.' }

function Read-Environment {
    param([string] $Path)
    $values = [ordered]@{}
    foreach ($line in [IO.File]::ReadAllLines($Path)) {
        if ($line.Trim().StartsWith('#') -or -not $line.Contains('=')) { continue }
        $separator = $line.IndexOf('=')
        $values[$line.Substring(0, $separator).Trim()] = $line.Substring($separator + 1).Trim()
    }
    return $values
}
function Protect-PrivateFile {
    param([string] $Path)
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User
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
        $lines = @('# Private P7 isolated MySQL integration-test configuration.')
        foreach ($name in $Values.Keys) { $lines += "$name=$($Values[$name])" }
        $bytes = $utf8.GetBytes(($lines -join "`n") + "`n")
        $stream.Write($bytes, 0, $bytes.Length)
        [Array]::Clear($bytes, 0, $bytes.Length)
    } finally { $stream.Dispose() }
    Protect-PrivateFile $Path
}
if (-not (Test-Path -LiteralPath $credentialPath)) {
    $bytes = [byte[]]::new(32)
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($bytes)
        $password = ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
    } finally { $random.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
    Write-PrivateEnvironment -Path $credentialPath -CreateNew -Values ([ordered]@{ BANKING_P7_MYSQL_TEST_PASSWORD = $password })
}
$credentials = Read-Environment $credentialPath
$password = $credentials['BANKING_P7_MYSQL_TEST_PASSWORD']
if ($password -notmatch '^[a-f0-9]{64}$') { throw 'Invalid existing isolated MySQL test credential; no credentials were replaced.' }
$source = Read-Environment $sourcePath
foreach ($specification in @(
    @('finance.mysql-test.env.local', 'qingrongjin_p7_test'),
    @('finance.mysql-rollback-test.env.local', 'qingrongjin_p7_rollback_test'),
    @('finance.mysql-core-test.env.local', 'qingrongjin_p7_core_test'),
    @('finance.mysql-acceptance-test.env.local', 'qingrongjin_p7_acceptance_test')
)) {
    $values = [ordered]@{}
    foreach ($name in $source.Keys) { $values[$name] = $source[$name] }
    $values['BANKING_DATABASE_DRIVER'] = 'mysql'
    $values['BANKING_MYSQL_HOST'] = '127.0.0.1'
    $values['BANKING_MYSQL_PORT'] = '13306'
    $values['BANKING_MYSQL_USER'] = 'banking_p7_migrator'
    $values['BANKING_MYSQL_PASSWORD'] = $password
    $values['BANKING_MYSQL_DATABASE'] = $specification[1]
    $values['BANKING_MYSQL_TLS'] = 'false'
    Write-PrivateEnvironment -Path (Join-Path $PSScriptRoot $specification[0]) -Values $values
}
# During another test run, retain existing grants and only add the fixed test
# schema grants. The separate verifier still rejects unexpected privileges.
$resetPrivilegesSQL = if ($AdditiveOnly) { '' } else { "REVOKE ALL PRIVILEGES, GRANT OPTION FROM 'banking_p7_migrator'@'%';" }
$sql = @"
CREATE DATABASE IF NOT EXISTS qingrongjin_p7_test CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS qingrongjin_p7_rollback_test CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS qingrongjin_p7_core_test CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS qingrongjin_p7_acceptance_test CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'banking_p7_migrator'@'%' IDENTIFIED BY '$password';
ALTER USER 'banking_p7_migrator'@'%' IDENTIFIED BY '$password';
$resetPrivilegesSQL
GRANT ALL PRIVILEGES ON qingrongjin_p7_test.* TO 'banking_p7_migrator'@'%';
GRANT ALL PRIVILEGES ON qingrongjin_p7_rollback_test.* TO 'banking_p7_migrator'@'%';
GRANT ALL PRIVILEGES ON qingrongjin_p7_core_test.* TO 'banking_p7_migrator'@'%';
GRANT ALL PRIVILEGES ON qingrongjin_p7_acceptance_test.* TO 'banking_p7_migrator'@'%';
"@
$process = [Diagnostics.Process]::new()
$process.StartInfo = [Diagnostics.ProcessStartInfo]::new()
$process.StartInfo.FileName = 'docker'
$shellCommand = 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --protocol=TCP --host=127.0.0.1 --user=root --batch --raw --silent'
$arguments = @('exec', '-i', $MySQLContainer, 'sh', '-c', $shellCommand)
$quotedArguments = foreach ($argument in $arguments) { '"' + (($argument -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"' }
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
    $process.StandardInput.Write($sql)
    $process.StandardInput.Close()
    if (-not $process.WaitForExit(30000)) { $process.Kill(); throw 'Isolated MySQL provisioning exceeded 30 seconds; existing business data was preserved.' }
    [void]$outputTask.GetAwaiter().GetResult()
    [void]$errorTask.GetAwaiter().GetResult()
    if ($process.ExitCode -ne 0) { throw 'Isolated MySQL provisioning failed; raw SQL errors were withheld to avoid exposing credentials.' }
} finally { $process.Dispose() }
[ordered]@{
    provisioned = $true
    additive_only = [bool]$AdditiveOnly
    databases = @('qingrongjin_p7_test', 'qingrongjin_p7_rollback_test', 'qingrongjin_p7_core_test', 'qingrongjin_p7_acceptance_test')
    role = 'banking_p7_migrator'
    files = @('finance.mysql-test.env.local', 'finance.mysql-rollback-test.env.local', 'finance.mysql-core-test.env.local', 'finance.mysql-acceptance-test.env.local')
    existing_business_schema_modified = $false
    container_restarted = $false
} | ConvertTo-Json -Compress
