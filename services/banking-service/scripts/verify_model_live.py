"""P3 real published Coze/model -> protected Banking API acceptance.

Uses persisted isolated MySQL financial fixtures from TestLifestyleMySQLAPI.
JWT/PAT stay in memory. Records actual results before assertions; no fallback.
"""
import argparse
import base64
import hashlib
import hmac
import json
from pathlib import Path
import time
import urllib.error
import urllib.request
import uuid

root=Path(__file__).resolve().parents[3]
out=root/'services/banking-service/output/module11';out.mkdir(parents=True,exist_ok=True)
p=argparse.ArgumentParser();p.add_argument('--restart',action='store_true');a=p.parse_args()
env={}
for line in (root/'deploy/banking/.env.local').read_text(encoding='utf8').splitlines():
 if '=' in line and not line.startswith('#'):
  k,v=line.split('=',1);env[k]=v.strip().strip('"').strip("'")
state=json.loads((out/'live-state.json' if a.restart else root/'services/banking-service/output/module10/fixture-state.json').read_text(encoding='utf8'))
run={'started_at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'real_model_executed':False,'passed':False,'restart':a.restart,'checks':{},'responses':{}}
runpath=out/('live-restart.json' if a.restart else 'live.json')
if runpath.exists():
 previous=json.loads(runpath.read_text(encoding='utf8'))
 if not previous.get('passed'):
  archived=out/(runpath.stem+'-failed-'+str(time.time_ns())+'.json')
  archived.write_bytes(runpath.read_bytes())
def record():runpath.write_text(json.dumps(run,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
record()
def token(role=None):
 now=int(time.time());claims={'iss':env.get('BANKING_JWT_ISSUER','qingyingjin'),'aud':[env.get('BANKING_JWT_AUDIENCE','banking-service')],'sub':state['user_id'],'iat':now,'nbf':now,'exp':now+(90 if role else 180),'jti':str(uuid.uuid4()),'token_use':'agent' if role else 'user'}
 if role:claims.update(agent_type=role,task_id='p3-live-signed')
 def enc(v):return base64.urlsafe_b64encode(json.dumps(v,separators=(',',':')).encode()).rstrip(b'=')
 unsigned=enc({'alg':'HS256','typ':'JWT'})+b'.'+enc(claims)
 return (unsigned+b'.'+base64.urlsafe_b64encode(hmac.new(env['BANKING_USER_JWT_SECRET'].encode(),unsigned,hashlib.sha256).digest()).rstrip(b'=')).decode()
def request(method,path,body=None,role=None,key=None):
 q=urllib.request.Request('http://127.0.0.1:8090'+path,data=json.dumps(body,ensure_ascii=False).encode() if body is not None else None,method=method,headers={'Authorization':'Bearer '+token(role),'Content-Type':'application/json','Idempotency-Key':key or str(uuid.uuid4()),'X-Trace-ID':'p3-real-model'})
 try:r=urllib.request.urlopen(q,timeout=75)
 except urllib.error.HTTPError as e:r=e
 with r:return r.status,json.loads(r.read())
def checked(name,method,path,body=None,status=200,**kw):
 code,v=request(method,path,body,**kw);run['responses'][name]={'http_status':code,'body':v};record()
 assert code==status,(name,code,v.get('code'));return v.get('data')

for path in ['/healthz','/readyz']:
 checked(path,'GET',path);run['checks'][path]=200
if not a.restart:
 before=checked('before','GET','/api/v1/accounts/'+state['account_id']);state['balance_minor']=before['balance_minor']
 text='未来4个月每月少花800元，可以调整shopping，禁止转账。'
 parsed=checked('parse','POST','/api/v2/lifestyle-goals/parse',{'text':text})
 assert parsed['status']=='parsed' and parsed['execute_id'] and parsed['workflow_id']==env['COZE_LIFESTYLE_GOAL_WORKFLOW_ID']
 spec=parsed['spec'];assert spec['period_months']==4 and spec['expense_reduction_monthly_minor']==80000 and spec['savings_monthly_minor']==0
 assert 'transfer' in spec['prohibited_operations'] and spec['adjustable_categories']==['shopping']
 state['parse_execute_id']=parsed['execute_id'];run['real_model_executed']=True;run['checks']['real_parse_exact_amounts']=True
 code,amb=request('POST','/api/v2/lifestyle-goals/parse',{'text':'想健康生活，少花些钱。'})
 run['responses']['ambiguous']={'http_status':code,'body':amb};record()
 assert (code==422 and amb['code']=='MODEL_OUTPUT_INVALID') or (code==200 and amb['data']['status']=='needs_information' and 'spec' not in amb['data'])
 run['checks']['ambiguous_goal_not_executable']=True
 goal=checked('goal','POST','/api/v2/lifestyle-goals',{'text':text,'spec':spec,'expected_version':0},status=201)
 state['goal_id']=goal['goal']['id'];state['expected_version']=1
 ci={'expected_version':1,'budget_id':state['budget_id'],'billing_report_id':state['billing_report_id'],'financial_plan_id':'0'}
 plan=checked('plan','POST','/api/v2/lifestyle-goals/'+state['goal_id']+'/compile',ci,status=201)
 state['plan_id']=plan['plan']['id'];state['result_digest']=hashlib.sha256(json.dumps(plan['plan']['result'],sort_keys=True).encode()).hexdigest()
 options=plan['plan']['result']['options'];assert len(options)==3
 for option in options:
  assert option['savings_monthly_minor']==0
  for category in option['categories']:
   if category['protected']:assert category['proposed_minor']==category['original_minor']
 assert not plan['plan']['result']['financial_execution_enabled']
 job=checked('submit','POST','/api/v1/orchestrations',{'instruction':'查询并解释已有生活方案，只安排一个 Lifestyle 查询任务。','bindings':{'lifestyle_plan_id':state['plan_id']},'use_coze':True,'max_attempts':1,'timeout_ms':60000,'strategy':'sequential'},status=201)
 state['job_id']=job['job']['id']
 (out/'live-state.json').write_text(json.dumps(state,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
 deadline=time.monotonic()+150
 while True:
  code,v=request('GET','/api/v1/orchestrations/'+state['job_id']);assert code==200
  if v['data']['job']['status'] in ['succeeded','failed','waiting_input','waiting_confirmation','cancelled']:
   run['responses']['job']={'http_status':code,'body':v};record();break
  assert time.monotonic()<deadline,v['data']['job']['status'];time.sleep(.5)
 assert v['data']['job']['status']=='succeeded',[(t['key'],t['status'],t['error_code']) for t in v['data']['tasks']]
 run['checks'].update(native_supervisor=True,three_safe_plans=True,protected_spending_preserved=True)

result=checked('result','GET','/api/v1/orchestrations/'+state['job_id']+'/result')
assert result['status']=='succeeded' and not result['financial_execution_enabled']
outputs=result['outputs'];lifestyle=next(o for o in outputs if o['agent_type']=='lifestyle')
assert lifestyle['workflow_execute_id'] and lifestyle['explanation'] and lifestyle['explanation_evidence_ids']
assert lifestyle['explanation_finding_ids'] and '没有交易记录' not in lifestyle['explanation']
assert set(lifestyle['explanation_evidence_ids']) <= {ev['id'] for ev in lifestyle['evidence']}
assert not lifestyle['financial_execution_enabled'] and lifestyle['skill_id']=='lifestyle_planning'
assert hashlib.sha256(json.dumps(lifestyle['result']['plan']['result'],sort_keys=True).encode()).hexdigest()==state['result_digest']
trace=checked('trace','GET','/api/v1/orchestrations/'+state['job_id']+'/trace')
assert len([e for e in trace['executions'] if e['status']=='succeeded' and e['workflow_execute_id']])==2
assert all(e['attempt']==1 for e in trace['executions'])
after=checked('after','GET','/api/v1/accounts/'+state['account_id']);assert after['balance_minor']==state['balance_minor']
checked('wrong_role','GET','/api/v2/lifestyle-plans/'+state['plan_id'],role='billing',status=403)
run['checks'].update(real_lifestyle_explanation=True,real_evidence_references=True,authoritative_amounts_unchanged=True,execution_ids_persisted=True,no_automatic_retry=True,balance_unchanged=True,wrong_role_403=True)
run['real_model_executed']=True;run['passed']=True
record();print(json.dumps(run['checks'],ensure_ascii=False))
