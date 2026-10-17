[CmdletBinding()]
param(
    [string] $PostgresContainer = 'qingrongjin-agent-banking-agent-postgres-1',
    [string] $MySQLContainer = 'qingyingjin-banking-mysql-1'
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
function Invoke-ReadOnlyQuery {
    param([string] $Container, [string] $ShellCommand, [string] $SQL)
    if ($Container -notmatch '^[a-zA-Z0-9_.-]+$') { throw 'Invalid database container name.' }
    $arguments = @('exec', '-i', $Container, 'sh', '-c', $ShellCommand)
    $quotedArguments = foreach ($argument in $arguments) { '"' + (($argument -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"' }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = [Diagnostics.ProcessStartInfo]::new()
    $process.StartInfo.FileName = 'docker'
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
        if (-not $process.WaitForExit(30000)) { $process.Kill(); throw 'Read-only access inspection exceeded 30 seconds.' }
        $output = $outputTask.GetAwaiter().GetResult()
        [void]$errorTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) { throw 'Read-only access inspection failed; raw database diagnostics were withheld.' }
        return $output.Trim() | ConvertFrom-Json
    } finally { $process.Dispose() }
}
$postgresSQL = @'
BEGIN READ ONLY;
SELECT json_build_object(
 'two_nonprivileged_login_roles', (SELECT count(*)=2 AND bool_and(rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls AND NOT rolinherit) FROM pg_roles WHERE rolname IN ('banking_finance_app','banking_finance_migrator')),
 'no_role_memberships', NOT EXISTS (SELECT 1 FROM pg_auth_members WHERE member IN (SELECT oid FROM pg_roles WHERE rolname IN ('banking_finance_app','banking_finance_migrator'))),
 'four_finance_databases_owned_by_migrator', (SELECT count(*)=4 AND bool_and(pg_get_userbyid(datdba)='banking_finance_migrator') FROM pg_database WHERE datname IN ('qingrongjin_finance','qingrongjin_finance_test','qingrongjin_finance_core_test','qingrongjin_finance_acceptance_test')),
 'app_cannot_connect_core_test', NOT has_database_privilege('banking_finance_app','qingrongjin_finance_core_test','CONNECT'),
 'app_cannot_connect_acceptance_test', NOT has_database_privilege('banking_finance_app','qingrongjin_finance_acceptance_test','CONNECT'),
 'app_can_connect_finance', has_database_privilege('banking_finance_app','qingrongjin_finance','CONNECT'),
 'app_cannot_create_finance_schema', NOT has_database_privilege('banking_finance_app','qingrongjin_finance','CREATE'),
 'app_cannot_create_temp_tables', NOT has_database_privilege('banking_finance_app','qingrongjin_finance','TEMP'),
 'app_cannot_connect_product', NOT has_database_privilege('banking_finance_app','qingrongjin_agent','CONNECT'),
 'migrator_cannot_connect_product', NOT has_database_privilege('banking_finance_migrator','qingrongjin_agent','CONNECT'),
 'app_can_use_public_schema', has_schema_privilege('banking_finance_app','public','USAGE'),
 'app_cannot_create_public_tables', NOT has_schema_privilege('banking_finance_app','public','CREATE'),
 'app_owns_no_schema', NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspowner=(SELECT oid FROM pg_roles WHERE rolname='banking_finance_app')),
 'app_owns_no_relations', NOT EXISTS (SELECT 1 FROM pg_class WHERE relowner=(SELECT oid FROM pg_roles WHERE rolname='banking_finance_app'))
);
ROLLBACK;
'@
$mysqlSQL = @'
START TRANSACTION READ ONLY;
SELECT JSON_OBJECT(
 'mysql_test_role_exists_once', (SELECT COUNT(*)=1 FROM mysql.user WHERE User='banking_p7_migrator' AND Host='%'),
 'mysql_test_role_has_no_global_privileges', (SELECT COUNT(*)=0 FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=CONCAT(CHAR(39),'banking_p7_migrator',CHAR(39),'@',CHAR(39),'%',CHAR(39)) AND PRIVILEGE_TYPE<>'USAGE'),
 'mysql_test_role_only_four_test_schemata', (SELECT COUNT(DISTINCT TABLE_SCHEMA)=4 AND SUM(TABLE_SCHEMA NOT IN ('qingrongjin_p7_test','qingrongjin_p7_rollback_test','qingrongjin_p7_core_test','qingrongjin_p7_acceptance_test'))=0 FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE=CONCAT(CHAR(39),'banking_p7_migrator',CHAR(39),'@',CHAR(39),'%',CHAR(39))),
 'mysql_test_role_cannot_grant', (SELECT COUNT(*)=0 FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE=CONCAT(CHAR(39),'banking_p7_migrator',CHAR(39),'@',CHAR(39),'%',CHAR(39)) AND IS_GRANTABLE='YES')
);
ROLLBACK;
'@
$postgres = Invoke-ReadOnlyQuery -Container $PostgresContainer -ShellCommand 'PGPASSWORD="$POSTGRES_PASSWORD" exec psql --no-psqlrc --host=127.0.0.1 --username=postgres --dbname=qingrongjin_finance --quiet --tuples-only --no-align --set=ON_ERROR_STOP=1' -SQL $postgresSQL
$mysql = Invoke-ReadOnlyQuery -Container $MySQLContainer -ShellCommand 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --protocol=TCP --host=127.0.0.1 --user=root --batch --raw --silent --skip-column-names' -SQL $mysqlSQL
$checks = [ordered]@{}
foreach ($inspection in @($postgres, $mysql)) {
    foreach ($property in $inspection.PSObject.Properties) {
        if (-not [bool]$property.Value) { throw "Database privilege check failed: $($property.Name)" }
        $checks[$property.Name] = $true
    }
}
[ordered]@{ passed = $true; count = $checks.Count; checks = $checks; read_only = $true; credentials_printed = $false } | ConvertTo-Json -Depth 3
