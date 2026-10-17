[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$checks = [Collections.Generic.List[string]]::new()
function Assert-Condition {
    param([bool] $Condition, [string] $Name)
    if (-not $Condition) { throw "P7 configuration check failed: $Name" }
    $checks.Add($Name)
}
function Read-Environment {
    param([string] $Path)
    $values = @{}
    foreach ($line in [IO.File]::ReadAllLines($Path)) {
        if ($line.Trim().StartsWith('#') -or -not $line.Contains('=')) { continue }
        $separator = $line.IndexOf('=')
        $values[$line.Substring(0, $separator).Trim()] = $line.Substring($separator + 1).Trim()
    }
    return $values
}
function Read-ComposeConfiguration {
    param([string] $Environment, [string[]] $Files)
    $arguments = @('compose', '--env-file', $Environment)
    foreach ($file in $Files) { $arguments += @('-f', $file) }
    # Resolved Compose contains credentials. Capture it in memory and print only
    # assertions, never its JSON or raw diagnostic output.
    $configuration = & docker @arguments config --format json 2>$null
    if ($LASTEXITCODE -ne 0) { throw 'Compose validation failed; raw configuration was withheld.' }
    return ($configuration -join "`n") | ConvertFrom-Json
}

$hostSource = Read-Environment (Join-Path $repositoryRoot 'deploy\banking\.env.local')
$environments = @{}
foreach ($specification in @(
    @('finance.migrate.env.local', 'banking_finance_migrator', 'qingrongjin_finance'),
    @('finance.test.env.local', 'banking_finance_migrator', 'qingrongjin_finance_test'),
    @('finance.core-test.env.local', 'banking_finance_migrator', 'qingrongjin_finance_core_test'),
    @('finance.acceptance-test.env.local', 'banking_finance_migrator', 'qingrongjin_finance_acceptance_test'),
    @('finance.app.env.local', 'banking_finance_app', 'qingrongjin_finance')
)) {
    $values = Read-Environment (Join-Path $PSScriptRoot $specification[0])
    Assert-Condition ($values.BANKING_DATABASE_DRIVER -eq 'postgres' -and $values.BANKING_POSTGRES_HOST -eq '127.0.0.1' -and $values.BANKING_POSTGRES_PORT -eq '15432' -and $values.BANKING_POSTGRES_SSLMODE -eq 'disable') ($specification[0] + ':local-postgres')
    Assert-Condition ($values.BANKING_POSTGRES_USER -eq $specification[1] -and $values.BANKING_POSTGRES_DATABASE -eq $specification[2] -and $values.BANKING_POSTGRES_PASSWORD -match '^[a-f0-9]{64}$' -and $values.BANKING_MYSQL_PASSWORD -eq '') ($specification[0] + ':role-and-database')
    foreach ($key in @('BANKING_USER_JWT_SECRET', 'BANKING_REDIS_PASSWORD', 'BANKING_COZE_PAT')) {
        Assert-Condition ($values[$key] -eq $hostSource[$key]) ($specification[0] + ':preserved-' + $key)
    }
    $environments[$specification[0]] = $values
}
Assert-Condition ($environments['finance.app.env.local'].BANKING_POSTGRES_PASSWORD -ne $environments['finance.migrate.env.local'].BANKING_POSTGRES_PASSWORD) 'independent-app-owner-credentials'
$importEnvironment = $environments['finance.test.env.local']
$coreEnvironment = $environments['finance.core-test.env.local']
$differentKeys = @($importEnvironment.Keys | Where-Object { $importEnvironment[$_] -ne $coreEnvironment[$_] })
Assert-Condition ($importEnvironment.Count -eq $coreEnvironment.Count -and $differentKeys.Count -eq 1 -and $differentKeys[0] -eq 'BANKING_POSTGRES_DATABASE') 'core-test-clones-import-configuration-except-database'
$acceptanceEnvironment = $environments['finance.acceptance-test.env.local']
$differentKeys = @($importEnvironment.Keys | Where-Object { $importEnvironment[$_] -ne $acceptanceEnvironment[$_] })
Assert-Condition ($importEnvironment.Count -eq $acceptanceEnvironment.Count -and $differentKeys.Count -eq 1 -and $differentKeys[0] -eq 'BANKING_POSTGRES_DATABASE') 'acceptance-test-clones-import-configuration-except-database'
$mysqlCredentials = Read-Environment (Join-Path $PSScriptRoot 'mysql-tests.credentials.env.local')
$mysqlReference = $null
foreach ($specification in @(
    @('finance.mysql-test.env.local', 'qingrongjin_p7_test'),
    @('finance.mysql-rollback-test.env.local', 'qingrongjin_p7_rollback_test'),
    @('finance.mysql-core-test.env.local', 'qingrongjin_p7_core_test'),
    @('finance.mysql-acceptance-test.env.local', 'qingrongjin_p7_acceptance_test')
)) {
    $values = Read-Environment (Join-Path $PSScriptRoot $specification[0])
    Assert-Condition ($values.BANKING_DATABASE_DRIVER -eq 'mysql' -and $values.BANKING_MYSQL_HOST -eq '127.0.0.1' -and $values.BANKING_MYSQL_PORT -eq '13306' -and $values.BANKING_MYSQL_TLS -eq 'false') ($specification[0] + ':local-mysql')
    Assert-Condition ($values.BANKING_MYSQL_USER -eq 'banking_p7_migrator' -and $values.BANKING_MYSQL_DATABASE -eq $specification[1] -and $values.BANKING_MYSQL_PASSWORD -match '^[a-f0-9]{64}$' -and $values.BANKING_MYSQL_PASSWORD -eq $mysqlCredentials.BANKING_P7_MYSQL_TEST_PASSWORD) ($specification[0] + ':canonical-role-and-database')
    if ($null -eq $mysqlReference) { $mysqlReference = $values } else {
        $differentKeys = @($mysqlReference.Keys | Where-Object { $mysqlReference[$_] -ne $values[$_] })
        Assert-Condition ($mysqlReference.Count -eq $values.Count -and $differentKeys.Count -eq 1 -and $differentKeys[0] -eq 'BANKING_MYSQL_DATABASE') ($specification[0] + ':configuration-differs-only-database')
    }
}
$banking = Read-ComposeConfiguration -Environment (Join-Path $PSScriptRoot 'finance.compose.env.local') -Files @((Join-Path $repositoryRoot 'deploy\banking\compose.yaml'), (Join-Path $PSScriptRoot 'compose.yaml'))
$runtime = $banking.services.'banking-service'
$migration = $banking.services.'banking-migrate'
Assert-Condition ($runtime.image -eq 'qingyingjin-banking-service:p7' -and $migration.image -eq $runtime.image) 'p7-image'
Assert-Condition ($runtime.environment.BANKING_POSTGRES_USER -eq 'banking_finance_app' -and $runtime.environment.BANKING_POSTGRES_PASSWORD -eq $environments['finance.app.env.local'].BANKING_POSTGRES_PASSWORD -and $runtime.environment.BANKING_MYSQL_PASSWORD -eq '') 'runtime-app-only'
Assert-Condition ($null -eq $runtime.environment.PSObject.Properties['BANKING_FINANCE_MIGRATOR_PASSWORD']) 'runtime-has-no-migrator-password'
Assert-Condition ($migration.environment.BANKING_POSTGRES_USER -eq 'banking_finance_migrator' -and $migration.environment.BANKING_POSTGRES_PASSWORD -eq $environments['finance.migrate.env.local'].BANKING_POSTGRES_PASSWORD) 'migration-owner-only'
Assert-Condition ($null -eq $runtime.depends_on.PSObject.Properties['banking-mysql'] -and $null -eq $migration.PSObject.Properties['depends_on']) 'no-mysql-readiness-dependency'
Assert-Condition ($null -ne $migration.networks.PSObject.Properties['banking-egress'] -and $null -ne $runtime.networks.PSObject.Properties['banking-egress']) 'finance-private-network'
$product = Read-ComposeConfiguration -Environment (Join-Path $repositoryRoot 'deploy\banking-agent\.env') -Files @((Join-Path $repositoryRoot 'deploy\banking-agent\compose.yaml'), (Join-Path $repositoryRoot 'deploy\banking-agent\compose.test.yaml'), (Join-Path $PSScriptRoot 'compose.product-postgres.yaml'))
$postgres = $product.services.'banking-agent-postgres'
Assert-Condition ($null -ne $postgres.networks.PSObject.Properties['agent-internal'] -and $null -ne $postgres.networks.PSObject.Properties['agent-test'] -and $null -ne $postgres.networks.PSObject.Properties['banking-private']) 'product-original-networks-preserved'
Assert-Condition (@($postgres.ports | Where-Object { $_.host_ip -eq '127.0.0.1' -and $_.published -eq '15432' -and $_.target -eq 5432 }).Count -eq 1) 'product-loopback-port-preserved'
Assert-Condition ($postgres.environment.POSTGRES_DB -eq 'qingrongjin_agent' -and $postgres.volumes[0].source -eq 'banking-agent-postgres-data') 'product-database-volume-preserved'
[ordered]@{ passed = $true; count = $checks.Count; checks = @($checks); business_writes = $false; service_restarts = $false } | ConvertTo-Json -Depth 3
