$ErrorActionPreference = 'Stop'
$bankingRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$runtimeDir = Join-Path $bankingRoot 'output/coze-runtime'
$denoFile = Join-Path $runtimeDir 'deno/deno'
if (!(Test-Path -LiteralPath $denoFile)) {
  New-Item -ItemType Directory -Path $runtimeDir -Force | Out-Null
  $archive = Join-Path $runtimeDir 'deno-linux.zip'
  Invoke-WebRequest 'https://github.com/denoland/deno/releases/download/v2.2.0/deno-x86_64-unknown-linux-gnu.zip' -OutFile $archive
  Expand-Archive -LiteralPath $archive -DestinationPath (Join-Path $runtimeDir 'deno') -Force
}
# Restore the unchanged runtime assets used by backend/Dockerfile, not domain code.
docker cp $denoFile coze-server-local:/usr/local/bin/deno
if ($LASTEXITCODE) { throw 'Cannot copy Deno into the local Coze container' }
docker cp (Join-Path $bankingRoot 'backend/infra/coderunner/impl/script/sandbox.py') coze-server-local:/app/sandbox.py
if ($LASTEXITCODE) { throw 'Cannot restore the native sandbox script' }
docker exec coze-server-local chmod +x /usr/local/bin/deno
docker exec coze-server-local deno run --lock=node_modules/banking-runtime.lock --node-modules-dir=auto --allow-read=node_modules --allow-write=node_modules --allow-net=cdn.jsdelivr.net,registry.npmjs.org,jsr.io --allow-env=HOME jsr:@langchain/pyodide-sandbox@0.0.4 -c "print('Sandbox warmup')"
if ($LASTEXITCODE) { throw 'Native sandbox warmup failed; do not disable sandbox isolation' }
