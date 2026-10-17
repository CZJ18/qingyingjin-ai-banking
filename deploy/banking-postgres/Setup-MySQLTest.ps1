param([string]$MySQLContainer='qingyingjin-banking-mysql-1')
throw 'Obsolete helper: use Setup-MySQLTests.ps1 (plural). This file is retained because automatic cleanup was rejected.'
$ErrorActionPreference='Stop'
$deploymentRoot=[IO.Path]::GetFullPath($PSScriptRoot)
$repoRoot=[IO.Path]::GetFullPath((Join-Path $deploymentRoot '../..'))
if($MySQLContainer -notmatch '^[a-zA-Z0-9_.-]+$'){throw 'Invalid container name'}
function Read-Env([string]$path){
    $m=[ordered]@{}
    foreach($line in [IO.File]::ReadAllLines($path)){
        if($line -match '^([A-Z][A-Z0-9_]*)=(.*)$'){$m[$Matches[1]]=$Matches[2]}
    }
    return $m
}
$credentialFile=Join-Path $deploymentRoot 'mysql-test.credentials.env.local'
if(-not(Test-Path -LiteralPath $credentialFile)){
    $bytes=New-Object byte[] 32
    $rng=[Security.Cryptography.RandomNumberGenerator]::Create()
    try{$rng.GetBytes($bytes);$password=([BitConverter]::ToString($bytes)).Replace('-','').ToLowerInvariant()}finally{$rng.Dispose()}
    [IO.File]::WriteAllText($credentialFile,"BANKING_P7_MYSQL_TEST_PASSWORD=$password`n",[Text.UTF8Encoding]::new($false))
}
$credentials=Read-Env $credentialFile
$password=$credentials['BANKING_P7_MYSQL_TEST_PASSWORD']
if($password -notmatch '^[a-f0-9]{64}$'){throw 'Invalid private test password format'}
$sql="CREATE USER IF NOT EXISTS 'banking_p7_migrator'@'%' IDENTIFIED BY '$password';`n"
foreach($database in @('qingrongjin_p7_test','qingrongjin_p7_core_test','qingrongjin_p7_rollback_test')){
    $sql+="CREATE DATABASE IF NOT EXISTS $database CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;`nGRANT ALL PRIVILEGES ON $database.* TO 'banking_p7_migrator'@'%';`n"
}
$process=[Diagnostics.Process]::new()
$process.StartInfo=[Diagnostics.ProcessStartInfo]::new()
$process.StartInfo.FileName='docker'
$shellCommand='MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root --batch --silent'
$quotedArguments=foreach($argument in @('exec','-i',$MySQLContainer,'sh','-c',$shellCommand)){
    '"'+(($argument -replace '(\\*)"','$1$1\"') -replace '(\\+)$','$1$1')+'"'
}
$process.StartInfo.Arguments=$quotedArguments -join ' '
$process.StartInfo.UseShellExecute=$false
$process.StartInfo.CreateNoWindow=$true
$process.StartInfo.RedirectStandardInput=$true
$process.StartInfo.RedirectStandardOutput=$true
$process.StartInfo.RedirectStandardError=$true
[void]$process.Start()
$stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
$process.StandardInput.Write($sql);$process.StandardInput.Close();$process.WaitForExit()
if($process.ExitCode -ne 0){throw 'Isolated MySQL test provisioning failed; no credentials printed'}
$source=Read-Env (Join-Path $repoRoot 'deploy/banking/.env.local')
foreach($item in @(@('finance.mysql-test.env.local','qingrongjin_p7_test'),@('finance.mysql-core-test.env.local','qingrongjin_p7_core_test'),@('finance.mysql-rollback-test.env.local','qingrongjin_p7_rollback_test'))){
    $m=[ordered]@{};foreach($key in $source.Keys){$m[$key]=$source[$key]}
    $m['BANKING_DATABASE_DRIVER']='mysql';$m['BANKING_MYSQL_HOST']='127.0.0.1';$m['BANKING_MYSQL_PORT']='13306'
    $m['BANKING_MYSQL_USER']='banking_p7_migrator';$m['BANKING_MYSQL_PASSWORD']=$password;$m['BANKING_MYSQL_DATABASE']=$item[1]
    $lines=foreach($key in $m.Keys){"$key=$($m[$key])"}
    [IO.File]::WriteAllText((Join-Path $deploymentRoot $item[0]),($lines -join "`n")+"`n",[Text.UTF8Encoding]::new($false))
}
Write-Output '{"created_or_verified":true,"isolated_mysql_databases":3,"business_authority_unchanged":true}'
