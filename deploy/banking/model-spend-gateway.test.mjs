import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildGatewayConfig, verifyGateway } from './model-spend-gateway.mjs'

const token='synthetic-gateway-access-not-real-1234567890'
const env={BANKING_ENVIRONMENT:'development',DEEPSEEK_GATEWAY_BASE_URL:'http://banking-model-gateway:8092/v1',DEEPSEEK_GATEWAY_ALLOWED_ORIGIN:'http://banking-model-gateway:8092',DEEPSEEK_GATEWAY_TOKEN:token,DEEPSEEK_MODEL:'synthetic-model',DEEPSEEK_MAX_TOKENS:'4096'}
test('explicit development gateway uses its own token and never the supplier key',()=>{
  const c=buildGatewayConfig({...env,DEEPSEEK_API_KEY:'supplier-secret-must-not-be-persisted'})
  assert.equal(c.apiKey,token);assert.equal(c.baseURL,env.DEEPSEEK_GATEWAY_BASE_URL);assert.equal(c.gateway,true)
  assert.ok(!JSON.stringify(c).includes('supplier-secret'))
})
test('gateway rejects implicit hosts, insecure production, ambiguous origins and reused credentials',()=>{
  for (const change of [{BANKING_ENVIRONMENT:'unknown'},{BANKING_ENVIRONMENT:'production'},{DEEPSEEK_GATEWAY_ALLOWED_ORIGIN:''},{DEEPSEEK_GATEWAY_BASE_URL:'http://banking-model-gateway:8092/v1?token=secret'},{DEEPSEEK_GATEWAY_BASE_URL:'http://banking-model-gateway.evil:8092/v1'},{DEEPSEEK_GATEWAY_TOKEN:'short'},{DEEPSEEK_API_KEY:token},{DEEPSEEK_MAX_TOKENS:'1e3'}])assert.throws(()=>buildGatewayConfig({...env,...change}))
  const c=buildGatewayConfig({...env,BANKING_ENVIRONMENT:'production',DEEPSEEK_GATEWAY_BASE_URL:'https://model-gateway.example/v1',DEEPSEEK_GATEWAY_ALLOWED_ORIGIN:'https://model-gateway.example'})
  assert.equal(c.gateway,true)
})
const proof={object:'list',data:[{id:'synthetic-model'}],model_spend_enforcement:{mode:'conservative_project_lifetime',project_id:'synthetic-project',policy_sha256:'a'.repeat(64),max_output_tokens:4096}}
test('verification is authenticated GET only and requires enforcement proof, not model availability alone',async()=>{
  const c=buildGatewayConfig(env);let count=0
  const ok=await verifyGateway(c,async(url,o)=>{count++;assert.equal(url,env.DEEPSEEK_GATEWAY_BASE_URL+'/models');assert.equal(o.method,'GET');assert.equal(o.headers.Authorization,'Bearer '+token);assert.equal(o.redirect,'error');return Response.json(proof)})
  assert.equal(count,1);assert.equal(ok.policy_sha256,'a'.repeat(64))
  for(const bad of [{data:proof.data},{...proof,model_spend_enforcement:{...proof.model_spend_enforcement,max_output_tokens:128}},{...proof,data:[{id:'other'}]}])await assert.rejects(verifyGateway(c,async()=>Response.json(bad)))
  await assert.rejects(verifyGateway(c,async()=>new Response(token,{status:401})),error=>!error.message.includes(token))
})
test('development can verify the same credential at an explicitly configured loopback port',async()=>{
  const c=buildGatewayConfig({...env,DEEPSEEK_GATEWAY_VERIFY_BASE_URL:'http://127.0.0.1:18160/v1'})
  assert.equal(c.baseURL,env.DEEPSEEK_GATEWAY_BASE_URL)
  await verifyGateway(c,async url=>{assert.equal(url,'http://127.0.0.1:18160/v1/models');return Response.json(proof)})
  assert.throws(()=>buildGatewayConfig({...env,DEEPSEEK_GATEWAY_VERIFY_BASE_URL:'http://another-service:18160/v1'}))
})
