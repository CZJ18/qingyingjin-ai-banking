// Explicit Coze binding to the separately authenticated monetary gateway.
const fail=code=>{throw new Error(code)}
export function buildGatewayConfig(env) {
  const environment=env.BANKING_ENVIRONMENT||'production'
  if(!['production','development','test'].includes(environment))fail('MODEL_GATEWAY_ENVIRONMENT_INVALID')
  let url
  try{url=new URL(env.DEEPSEEK_GATEWAY_BASE_URL)}catch{fail('MODEL_GATEWAY_ORIGIN_INVALID')}
  if(!['https:','http:'].includes(url.protocol)||url.username||url.password||url.search||url.hash||url.pathname!='/v1'||url.origin!==env.DEEPSEEK_GATEWAY_ALLOWED_ORIGIN||url.origin+'/v1'!==env.DEEPSEEK_GATEWAY_BASE_URL||environment==='production'&&url.protocol!=='https:')fail('MODEL_GATEWAY_ORIGIN_INVALID')
  const apiKey=env.DEEPSEEK_GATEWAY_TOKEN||''
  if(!/^[\x21-\x7e]{32,512}$/.test(apiKey)||/CHANGE_ME|placeholder|your[-_ ]?api/i.test(apiKey)||apiKey===env.DEEPSEEK_API_KEY)fail('MODEL_GATEWAY_CREDENTIAL_INVALID')
  const model=env.DEEPSEEK_MODEL||''
  if(!/^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/.test(model))fail('MODEL_GATEWAY_MODEL_REQUIRED')
  const count=env.DEEPSEEK_MAX_TOKENS||''
  if(!/^[1-9][0-9]{0,3}$/.test(count)||Number(count)>8192)fail('MODEL_GATEWAY_TOKEN_BOUND_INVALID')
  const deadline=env.DEEPSEEK_WORKFLOW_TIMEOUT_MS
  if(deadline!==undefined&&(!/^\d+$/.test(deadline)||Number(deadline)<30000||Number(deadline)>180000))fail('MODEL_GATEWAY_TIMEOUT_INVALID')
  let verifyBaseURL=url.href
  if(env.DEEPSEEK_GATEWAY_VERIFY_BASE_URL){
    let verification
    try{verification=new URL(env.DEEPSEEK_GATEWAY_VERIFY_BASE_URL)}catch{fail('MODEL_GATEWAY_VERIFY_ORIGIN_INVALID')}
    if(environment==='production'||!['127.0.0.1','[::1]'].includes(verification.hostname)||!['http:','https:'].includes(verification.protocol)||verification.pathname!='/v1'||verification.username||verification.password||verification.search||verification.hash)fail('MODEL_GATEWAY_VERIFY_ORIGIN_INVALID')
    verifyBaseURL=verification.href
  }
  return {apiKey,baseURL:url.href,verifyBaseURL,model,maxTokens:Number(count),gateway:true,...(deadline===undefined?{}:{workflowTimeoutMs:Number(deadline)})}
}
export async function verifyGateway(config,fetcher=fetch) {
  let response
  try{response=await fetcher((config.verifyBaseURL||config.baseURL)+'/models',{method:'GET',headers:{Authorization:'Bearer '+config.apiKey,Accept:'application/json'},redirect:'error',signal:AbortSignal.timeout(20000)})}catch{fail('MODEL_GATEWAY_UNREACHABLE')}
  if(!response.ok||!response.body)fail('MODEL_GATEWAY_UNVERIFIED')
  let bytes=0,text='';const decoder=new TextDecoder()
  for await(const chunk of response.body){bytes+=chunk.byteLength;if(bytes>64*1024)fail('MODEL_GATEWAY_PROOF_INVALID');text+=decoder.decode(chunk,{stream:true})}text+=decoder.decode()
  let body
  try{body=JSON.parse(text)}catch{fail('MODEL_GATEWAY_PROOF_INVALID')}
  const proof=body.model_spend_enforcement
  if(!Array.isArray(body.data)||body.data.length!==1||body.data[0]?.id!==config.model||proof?.mode!=='conservative_project_lifetime'||!/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(proof.project_id)||!/^[a-f0-9]{64}$/.test(proof.policy_sha256)||!Number.isSafeInteger(proof.max_output_tokens)||config.maxTokens>proof.max_output_tokens)fail('MODEL_GATEWAY_PROOF_INVALID')
  return proof
}
