[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$destination = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'sms.env.local'))
if (-not (Test-Path -LiteralPath $destination -PathType Leaf)) {
    $secretBytes = [byte[]]::new(32)
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($secretBytes) } finally { $generator.Dispose() }
    $secret = [BitConverter]::ToString($secretBytes).Replace('-','').ToLowerInvariant()
    $stream = [IO.File]::Open($destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try {
        $acl=Get-Acl -LiteralPath $destination
        $acl.SetAccessRuleProtection($true,$false)
        foreach ($sid in @([Security.Principal.WindowsIdentity]::GetCurrent().User,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'))) {
            $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','Allow'))
        }
        Set-Acl -LiteralPath $destination -AclObject $acl
        $content="BANKING_SMS_PROVIDER=development`nBANKING_SMS_OTP_SECRET=$secret`nBANKING_AUTH_COOKIE_SECURE=false`n"
        $bytes=[Text.UTF8Encoding]::new($false).GetBytes($content)
        $stream.Write($bytes,0,$bytes.Length)
    } finally { $stream.Dispose() }
    Write-Output 'Created private local SMS development configuration; no real SMS provider configured.'
} else {
    Write-Output 'Preserved existing private SMS development configuration; no key replacement.'
}
