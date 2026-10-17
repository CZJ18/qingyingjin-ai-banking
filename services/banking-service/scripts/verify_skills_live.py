"""Read-only Skill deployment/restart acceptance; credentials never written out.

Creates isolated users, analysis jobs and an unparsed goal. No money operations.
Run from the banking-service directory after deploying the module9 image.
"""
import argparse
import base64
import hashlib
import hmac
import json
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.request
import uuid

parser=argparse.ArgumentParser()
parser.add_argument('--restart',action='store_true')
args=parser.parse_args()
root=Path(__file__).resolve().parents[3]
env={}
for line in (root/'deploy/banking/.env.local').read_text(encoding='utf-8').splitlines():
    if '=' in line and not line.startswith('#'):
        k,v=line.split('=',1);env[k]=v.strip().strip('"').strip("'")
output=Path(__file__).resolve().parents[1]/'output/module9'
statefile=output/'live-state.json'
base='http://127.0.0.1:8090'
state=json.loads(statefile.read_text(encoding='utf-8')) if args.restart else {'user_id':str(secrets.randbelow(10**12)+10**12)}
def token(role=None):
    now=int(time.time())
    claims={'iss':env.get('BANKING_JWT_ISSUER','qingyingjin'),'aud':[env.get('BANKING_JWT_AUDIENCE','banking-service')],'sub':state['user_id'],'iat':now,'nbf':now,'exp':now+(90 if role else 300),'jti':str(uuid.uuid4()),'token_use':'agent' if role else 'user'}
    if role:claims.update(agent_type=role,task_id='signed-module9-live')
    def enc(value):return base64.urlsafe_b64encode(json.dumps(value,separators=(',',':')).encode()).rstrip(b'=')
    unsigned=enc({'alg':'HS256','typ':'JWT'})+b'.'+enc(claims)
    sig=base64.urlsafe_b64encode(hmac.new(env['BANKING_USER_JWT_SECRET'].encode(),unsigned,hashlib.sha256).digest()).rstrip(b'=')
    return (unsigned+b'.'+sig).decode()
def request(method,path,body=None,role=None,key=None):
    raw=json.dumps(body,ensure_ascii=False).encode() if body is not None else None
    q=urllib.request.Request(base+path,data=raw,method=method,headers={'Authorization':'Bearer '+token(role),'Content-Type':'application/json','Idempotency-Key':key or str(uuid.uuid4()),'X-Task-ID':'client-supplied-task','X-Trace-ID':'module9-live'})
    try:r=urllib.request.urlopen(q,timeout=45)
    except urllib.error.HTTPError as e:r=e
    with r:return r.status,json.loads(r.read())

checks={}
for path in ['/healthz','/readyz']:
    status,r=request('GET',path);assert status==200,(path,status,r.get('code'));checks[path]=status
with urllib.request.urlopen(base+'/openapi.yaml',timeout=5) as r:
    served=r.read()
local=(root/'services/banking-service/api/openapi.yaml').read_bytes()
assert served==local,'deployed OpenAPI differs from source'
checks['openapi_sha256']=hashlib.sha256(served).hexdigest()

if args.restart:
    status,r=request('GET','/api/v1/orchestrations/'+state['job_id']);assert status==200
    job=r['data'];assert job['job']['status']=='succeeded'
    t=job['tasks'][0];assert t['skill_id']=='billing_analysis' and t['skill_version']==1 and t['attempts']==1
    status,r=request('GET','/api/v1/orchestrations/'+state['job_id']+'/trace');assert status==200 and len(r['data']['executions'])==1
    checks.update(restart_preserves_binding=True,execution_count=1)
    if state.get('goal_id'):
        status,r=request('GET','/api/v1/goals/'+state['goal_id']);assert status==200
        v=r['data']['version'];assert v['skill_id']=='goal_compilation' and v['skill_version']==1
        assert v['parse_id']==state['goal_parse_id'] and v['execute_id']==state['goal_execute_id']
        checks['goal_attempt_preserved']=True
else:
    status,r=request('POST','/api/v1/users/me',{'display_name':'Module9 isolated acceptance'});assert status==201,(status,r.get('code'))
    status,r=request('GET','/api/v1/skills');assert status==200 and len(r['data'])==6
    checks['catalog_count']=len(r['data'])
    status,r=request('POST','/api/v1/subscriptions/detect_subscriptions',{},role='billing');assert status==403 and r['code']=='FORBIDDEN' and r['task_id']=='signed-module9-live'
    checks['cross_role_denied']=True
    task=dict(key='billing',agent_type='billing',tool='get_transactions',skill_id='billing_analysis',skill_version=1,parameters={'from':'2026-01-01T00:00:00Z','to':'2026-02-01T00:00:00Z'},dependencies=[],priority=0,execution_mode='read_only')
    invalid=dict(task,skill_version=99)
    status,r=request('POST','/api/v1/orchestrations',{'tasks':[invalid]});assert status==422 and r['code']=='INVALID_TASK_PLAN'
    checks['unknown_version_denied']=True
    state['idempotency_key']=str(uuid.uuid4())
    status,r=request('POST','/api/v1/orchestrations',{'tasks':[task]},key=state['idempotency_key']);assert status==201,(status,r.get('code'))
    state['job_id']=r['data']['job']['id'];statefile.write_text(json.dumps(state,indent=2),encoding='utf-8')
    for _ in range(40):
        status,r=request('GET','/api/v1/orchestrations/'+state['job_id']);assert status==200
        if r['data']['job']['status'] in ['succeeded','failed','review_required']:break
        time.sleep(.5)
    job=r['data'];assert job['job']['status']=='succeeded',job['job']['status']
    task=job['tasks'][0];assert task['skill_id']=='billing_analysis' and task['skill_version']==1
    assert task['output']['evidence'],'no persisted report evidence'
    checks.update(job_id=state['job_id'],skill_id=task['skill_id'],skill_version=task['skill_version'],evidence=task['output']['evidence'])
    # Real configured Coze specialist explanation. Failure is recorded truthfully.
    coze_task=dict(key='coze_billing',agent_type='billing',tool='get_transactions',parameters={'from':'2026-01-01T00:00:00Z','to':'2026-02-01T00:00:00Z'})
    status,r=request('POST','/api/v1/orchestrations',{'tasks':[coze_task],'use_coze':True,'max_attempts':1,'timeout_ms':35000})
    checks['coze_submit_status']=status
    if status==201:
        coze_id=r['data']['job']['id'];state['coze_job_id']=coze_id
        for _ in range(40):
            _,r=request('GET','/api/v1/orchestrations/'+coze_id)
            if r['data']['job']['status'] in ['succeeded','failed','review_required']:break
            time.sleep(1)
        coze_job=r['data'];checks['coze_status']=coze_job['job']['status']
        checks['coze_errors']=[t['error_code'] for t in coze_job['tasks'] if t['error_code']]
        checks['coze_execute_ids']=[t.get('output',{}).get('workflow_execute_id','') for t in coze_job['tasks']]
    goal_input={'text':'未来3个月，每月储蓄200元，每月收入6000元，每月生活预算3500元，每月必要开支2500元，保持正常饮食和规律运动。','financial_context':{'monthly_income_minor':600000,'spending_budget_minor':350000,'essential_expenses_minor':250000}}
    status,r=request('POST','/api/v1/goals',goal_input)
    checks['goal_create_status']=status
    if status==201:
        goal_id=r['data']['goal']['id'];state['goal_id']=goal_id
        status,r=request('POST','/api/v1/goals/'+goal_id+'/parse',{'expected_version':1})
        checks['goal_parse_http']=status;checks['goal_parse_code']=r.get('code')
        status,r=request('GET','/api/v1/goals/'+goal_id)
        if status==200:
            version=r['data']['version']
            state['goal_parse_id']=version['parse_id'];state['goal_execute_id']=version['execute_id']
            checks['goal_skill']={k:version.get(k) for k in ['skill_id','skill_version','parse_status','parse_id','execute_id','audit_id']}
    statefile.write_text(json.dumps(state,indent=2),encoding='utf-8')

name='live-restart.json' if args.restart else 'live.json'
(output/name).write_text(json.dumps(checks,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(checks,ensure_ascii=False))
