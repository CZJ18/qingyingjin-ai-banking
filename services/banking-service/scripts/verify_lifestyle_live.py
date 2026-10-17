"""P2 real Docker API acceptance using isolated SQL fixtures; no execution tool.
Secrets remain in process memory. --restart checks the same durable resources.
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

parser=argparse.ArgumentParser();parser.add_argument('--restart',action='store_true');args=parser.parse_args()
root=Path(__file__).resolve().parents[3];out=root/'services/banking-service/output/module10'
env={}
for line in (root/'deploy/banking/.env.local').read_text(encoding='utf-8').splitlines():
 if '=' in line and not line.startswith('#'):
  k,v=line.split('=',1);env[k]=v.strip().strip('"').strip("'")
state=json.loads((out/('live-state.json' if args.restart else 'fixture-state.json')).read_text(encoding='utf-8'))
def token(role=None):
 now=int(time.time());c={'iss':env.get('BANKING_JWT_ISSUER','qingyingjin'),'aud':[env.get('BANKING_JWT_AUDIENCE','banking-service')],'sub':state['user_id'],'iat':now,'nbf':now,'exp':now+90,'jti':str(uuid.uuid4()),'token_use':'agent' if role else 'user'}
 if role:c.update(agent_type=role,task_id='signed-p2-live')
 def enc(v):return base64.urlsafe_b64encode(json.dumps(v,separators=(',',':')).encode()).rstrip(b'=')
 unsigned=enc({'alg':'HS256','typ':'JWT'})+b'.'+enc(c)
 return (unsigned+b'.'+base64.urlsafe_b64encode(hmac.new(env['BANKING_USER_JWT_SECRET'].encode(),unsigned,hashlib.sha256).digest()).rstrip(b'=')).decode()
def request(method,path,body=None,role=None,key=None):
 q=urllib.request.Request('http://127.0.0.1:8090'+path,data=json.dumps(body,ensure_ascii=False).encode() if body is not None else None,method=method,headers={'Authorization':'Bearer '+token(role),'Content-Type':'application/json','Idempotency-Key':key or str(uuid.uuid4()),'X-Trace-ID':'p2-live','X-Task-ID':'unsigned-client-task'})
 try:r=urllib.request.urlopen(q,timeout=20)
 except urllib.error.HTTPError as e:r=e
 with r:return r.status,json.loads(r.read())
checks={}
for path in ['/healthz','/readyz']:
 status,r=request('GET',path);assert status==200,(path,status,r['code']);checks[path]=status
with urllib.request.urlopen('http://127.0.0.1:8090/openapi.yaml') as r:raw=r.read()
assert raw==(root/'services/banking-service/api/openapi.yaml').read_bytes();checks['openapi_sha256']=hashlib.sha256(raw).hexdigest()
goalpath='/api/v2/lifestyle-goals/'+state['goal_id']
status,g=request('GET',goalpath);assert status==200 and g['data']['goal']['current_version']==state['expected_version']
if not args.restart:
 status,before=request('GET','/api/v1/accounts/'+state['account_id']);assert status==200
 state['balance_minor']=before['data']['balance_minor']
 ci={'expected_version':state['expected_version'],'budget_id':state['budget_id'],'billing_report_id':state['billing_report_id'],'financial_plan_id':'0'}
 status,r=request('POST',goalpath+'/compile',ci);assert status==201,(status,r['code'])
 view=r['data'];result=view['plan']['result'];assert len(result['options'])==3 and result['reduction_gap_minor']==40000 and not result['financial_execution_enabled']
 for option in result['options']:
  assert option['categories'][0]['proposed_minor']==80000 and option['categories'][0]['protected']
  assert option['savings_monthly_minor']==0
 state['plan_id']=view['plan']['id'];rec=view['recommendations'][2];state['recommendation_id']=rec['id']
 di={'expected_version':rec['version'],'decision':'accept','plan_digest':view['plan']['input_digest']};key=str(uuid.uuid4())
 decisionpath='/api/v2/recommendations/'+rec['id']+'/decisions'
 status,r=request('POST',decisionpath,di,role='lifestyle');assert status==403
 status,r=request('POST',decisionpath,di,key=key);assert status==200,(status,r['code']);state['decision_id']=r['data']['id'];assert int(state['decision_id'])>0
 status,r=request('POST',decisionpath,di,key=key);assert status==200 and r['data']['id']==state['decision_id']
 status,r=request('GET','/api/v2/lifestyle-plans/'+state['plan_id'],role='billing');assert status==403
 status,r=request('POST','/api/v2/lifestyle-goals/parse',{'text':'三个月健康生活'});assert status==503 and r['code']=='COZE_UNAVAILABLE'
 submit={'tasks':[{'key':'lifestyle','skill_id':'lifestyle_planning','skill_version':1,'agent_type':'lifestyle','tool':'compile_lifestyle_plan','parameters':dict(ci,goal_id=state['goal_id']),'execution_mode':'advice'}],'use_coze':False,'strategy':'sequential'}
 status,r=request('POST','/api/v1/orchestrations',submit);assert status==201,(status,r['code']);state['job_id']=r['data']['job']['id']
 deadline=time.monotonic()+20
 while True:
  status,r=request('GET','/api/v1/orchestrations/'+state['job_id']);assert status==200
  if r['data']['job']['status']=='succeeded':break
  assert time.monotonic()<deadline,r['data']['job']['status'];time.sleep(.25)
 (out/'live-state.json').write_text(json.dumps(state,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
 checks.update(three_plans=True,meal_budget_protected=True,expense_savings_separate=True,decision_not_authorization=True,idempotent_decision=True,wrong_role_403=True,unpublished_v2_model_503=True)
status,r=request('GET','/api/v2/lifestyle-plans/'+state['plan_id']);assert status==200
assert any(x['id']==state['recommendation_id'] and x['status']=='accepted' for x in r['data']['recommendations'])
status,r=request('GET','/api/v2/recommendations/'+state['recommendation_id']+'/decisions');assert status==200 and len(r['data'])==1 and r['data'][0]['id']==state['decision_id']
status,r=request('GET','/api/v1/orchestrations/'+state['job_id']);assert status==200 and r['data']['job']['status']=='succeeded';task=r['data']['tasks'][0];assert task['skill_id']=='lifestyle_planning' and task['attempts']==1
status,after=request('GET','/api/v1/accounts/'+state['account_id']);assert status==200 and after['data']['balance_minor']==state['balance_minor']
checks.update(durable_goal=True,durable_plan=True,durable_decision=True,task_attempts=1,agent_advisory_compile=True,balance_unchanged=True,real_model_v2_executed=False)
(out/('live-restart.json' if args.restart else 'live.json')).write_text(json.dumps(checks,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(checks,ensure_ascii=False))
