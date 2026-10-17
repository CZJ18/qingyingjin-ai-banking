"""Append module7 contracts; plugin exposes owner-bound observation only."""
import copy
from pathlib import Path
import yaml

root = Path(__file__).resolve().parents[1]
target = root / 'api/openapi.yaml'
doc = yaml.safe_load(target.read_text(encoding='utf-8'))
s = doc['components']['schemas']
STR = {'type': 'string'}
INT = {'type': 'integer', 'format': 'int64'}
BOOL = {'type': 'boolean'}
ID = {'type': 'string', 'pattern': '^[1-9][0-9]*$'}
UUID = {'type': 'string', 'format': 'uuid'}
RAW = {'type': 'object', 'additionalProperties': True}
FALSE = {'type': 'boolean', 'enum': [False]}
def ref(n): return {'$ref': '#/components/schemas/' + n}
def arr(v): return {'type': 'array', 'items': v}
def obj(p, optional=()): return {'type': 'object', 'additionalProperties': False, 'properties': p, 'required': [k for k in p if k not in optional]}
def schema(n,p,optional=()): s[n] = obj(p,optional)
schema('OrchestrationTaskSpec', {'skill_id': STR, 'skill_version': dict(INT,minimum=1), 'key': STR, 'agent_type': {'type': 'string', 'enum': ['supervisor','billing','subscription','savings']}, 'tool': STR, 'parameters': RAW, 'dependencies': arr(STR), 'priority': dict(INT,minimum=0,maximum=1000), 'execution_mode': STR, 'blocked': BOOL}, ('skill_id','skill_version','parameters','dependencies','priority','execution_mode','blocked'))
schema('OrchestrationBindings', {k: STR for k in ['from','to','account_id','financial_plan_id']}, ('from','to','account_id','financial_plan_id'))
schema('OrchestrationSubmitInput', {'instruction': STR, 'task_plan_id': ID, 'tasks': arr(ref('OrchestrationTaskSpec')), 'bindings': ref('OrchestrationBindings'), 'strategy': {'type': 'string', 'enum': ['sequential','parallel_read']}, 'max_attempts': dict(INT,minimum=1,maximum=3), 'timeout_ms': dict(INT,minimum=100,maximum=60000), 'use_coze': BOOL}, ('instruction','task_plan_id','tasks','bindings','strategy','max_attempts','timeout_ms','use_coze'))
schema('OrchestrationConfirmInput', {'task_key': STR, 'expected_version': dict(INT,minimum=1), 'decision': {'type': 'string', 'enum': ['approve','reject']}})
schema('OrchestrationInputsInput', {'task_key': STR,'expected_version': dict(INT,minimum=1),'parameters': RAW})
schema('OrchestrationIDInput', {'job_id': UUID})
schema('OrchestrationJob', {'id': UUID,'user_id': ID,'status': STR,'strategy': STR,'version': INT,'max_attempts': INT,'timeout_ms': INT,'use_coze': BOOL,'source_plan_id': STR,'lease_until': INT,'trace_id': STR,'audit_id': STR,'created_at': INT,'updated_at': INT})
schema('OrchestrationTask', {'skill_id':STR,'skill_version':INT,'protected_blocked': BOOL,'task_id': UUID,'user_id': ID,'job_id': UUID,'key': STR,'agent_type': STR,'tool': STR,'execution_mode': STR,'priority': INT,'status': STR,'parameters': RAW,'output': RAW,'attempts': INT,'retry_at': INT,'confirmation': STR,'error_code': STR,'created_at': INT,'updated_at': INT})
schema('OrchestrationDependency', {'id': ID,'user_id': ID,'job_id': UUID,'task_id': UUID,'depends_on': UUID})
schema('OrchestrationExecution', {'id': UUID,'user_id': ID,'job_id': UUID,'task_id': UUID,'agent_type': STR,'attempt': INT,'status': STR,'started_at': INT,'finished_at': INT,'duration_ms': INT,'error_code': STR,'workflow_execute_id': STR,'input': RAW,'output': RAW})
schema('OrchestrationInvocation', {'id': UUID,'user_id': ID,'job_id': UUID,'task_id': UUID,'execution_id': UUID,'tool': STR,'invocation_key': STR,'status': STR,'started_at': INT,'finished_at': INT,'duration_ms': INT,'error_code': STR,'audit_id': STR})
schema('OrchestrationTrace', {'skill_id':STR,'skill_version':INT,'id': ID,'user_id': ID,'job_id': UUID,'task_id': STR,'event': STR,'status': STR,'error_code': STR,'audit_id': STR,'trace_id': STR,'created_at': INT})
schema('OrchestrationEvidence', {'type': STR,'id': STR,'audit_id': STR})
schema('OrchestrationError', {'code': STR,'retryable': BOOL})
schema('OrchestrationNextAction', {'type': STR,'requires_confirmation': BOOL})
schema('OrchestrationAgentOutput', {'skill_id':STR,'skill_version':INT,'rule_version':STR,'task_id': UUID,'agent_type': STR,'status': STR,'result': RAW,'evidence': arr(ref('OrchestrationEvidence')),'errors': arr(ref('OrchestrationError')),'next_actions': arr(ref('OrchestrationNextAction')),'explanation': STR,'workflow_execute_id': STR,'financial_execution_enabled': FALSE}, ('skill_id','skill_version','rule_version'))
schema('OrchestrationView', {'job': ref('OrchestrationJob'),'tasks': arr(ref('OrchestrationTask')),'dependencies': arr(ref('OrchestrationDependency')),'financial_execution_enabled': FALSE})
schema('OrchestrationResult', {'job_id': UUID,'status': STR,'outputs': arr(ref('OrchestrationAgentOutput')),'financial_execution_enabled': FALSE})
schema('OrchestrationTraceView', {'events': arr(ref('OrchestrationTrace')),'executions': arr(ref('OrchestrationExecution')),'invocations': arr(ref('OrchestrationInvocation'))})
for n in ['OrchestrationView','OrchestrationResult','OrchestrationTraceView']:
    e = copy.deepcopy(s['Envelope']); e['properties']['data'] = ref(n); e['required'] = list(dict.fromkeys(e.get('required',[])+['data'])); s[n+'Response'] = e
def route(path,method,opid,body=None,result='OrchestrationView',status='200',idem=False):
    params = [{'$ref':'#/components/parameters/TraceID'},{'$ref':'#/components/parameters/TaskID'}]
    if '{id}' in path: params.append({'name':'id','in':'path','required':True,'schema':UUID})
    if idem: params.append({'$ref':'#/components/parameters/IdempotencyKey'})
    op={'operationId':opid,'tags':['Orchestrator'],'security':[{'BankingToolKey' if path.startswith('/tools/') else 'UserBearer':[]}],'parameters':params,'description':'Persistent owner-scoped supervisory DAG. Parallelism only for read_only tools. Recommendations, proposals, explicit confirmation and financial execution remain separate. Module8 proposal gateway fails closed when absent; this module never moves funds. Coze use requires published native workflow IDs. No user identity or credentials are accepted from model parameters.','responses':{}}
    if body: op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
    for code,name,description in [(status,result+'Response','Success'),('400','Envelope','INVALID_ARGUMENT'),('401','Envelope','UNAUTHENTICATED'),('403','Envelope','FORBIDDEN'),('404','Envelope','NOT_FOUND including unowned task'),('409','Envelope','VERSION_CONFLICT / STATE_CONFLICT / IDEMPOTENCY_CONFLICT'),('422','Envelope','INVALID_TASK_PLAN / TOOL_RESULT_INVALID / GATEWAY_UNAVAILABLE / INPUT_REQUIRED'),('503','Envelope','DEPENDENCY_UNAVAILABLE / COZE_UNAVAILABLE')]: op['responses'][code]={'description':description,'content':{'application/json':{'schema':ref(name)}}}
    doc['paths'].setdefault(path,{})[method]=op
p='/api/v1/orchestrations'
route(p,'post','submit_orchestration','OrchestrationSubmitInput',status='201',idem=True)
route(p+'/{id}','get','get_user_orchestration')
route(p+'/{id}/result','get','get_user_orchestration_result',result='OrchestrationResult')
route(p+'/{id}/trace','get','get_orchestration_trace',result='OrchestrationTraceView')
doc['paths'][p+'/{id}/trace']['get']['parameters'] += [{'name':'last_event_id','in':'query','schema':dict(INT,minimum=0)},{'name':'limit','in':'query','schema':dict(INT,minimum=1,maximum=100,default=100)}]
for action in ['cancel','pause','resume']: route(p+'/{id}/'+action,'post',action+'_orchestration')
route(p+'/{id}/confirm','post','confirm_orchestration_task','OrchestrationConfirmInput')
route(p+'/{id}/inputs','post','supply_orchestration_task_inputs','OrchestrationInputsInput')
route(p+'/{id}/events','get','stream_orchestration_events')
stream=doc['paths'][p+'/{id}/events']['get']
stream['parameters'].append({'name':'Last-Event-ID','in':'header','schema':dict(STR,pattern='^(0|[1-9][0-9]*)$')})
stream['description']='Owner-authenticated SSE. Durable event IDs, task metadata only. Replay exclusive Last-Event-ID, heartbeat, bounded 30-second connection. Reconnect after end/reconnect; terminal end follows complete replay. No raw financial inputs/results in SSE.'
stream['responses']['200']={'description':'task events and end event; metadata JSON in SSE data','content':{'text/event-stream':{'schema':STR}}}
for tool,result in [('get_orchestration','OrchestrationView'),('get_orchestration_result','OrchestrationResult')]: route('/tools/v1/orchestrations/'+tool,'post',tool,'OrchestrationIDInput',result=result)
route('/api/v1/financial-plans/{id}/replan','post','replan_user_financial_plan',result='FinancialPlanView')
doc['paths']['/api/v1/financial-plans/{id}/replan']['post']['parameters'][-1]['schema']=ID
doc['info']['version']='0.7.0';doc['tags']=[t for t in doc.get('tags',[]) if t['name']!='Orchestrator']+[{'name':'Orchestrator'}]
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
pt=root/'api/coze-plugin.openapi.yaml';plugin=yaml.safe_load(pt.read_text(encoding='utf-8'));plugin['info']['version']='0.7.0'
for path,item in doc['paths'].items():
    if path.startswith('/tools/v1/orchestrations/'): plugin['paths'][path]=item
for n,v in s.items():
    if n.startswith('Orchestration'): plugin['components']['schemas'][n]=v
pt.write_text(yaml.safe_dump(plugin,allow_unicode=True,sort_keys=False),encoding='utf-8')
print('Orchestrator contracts generated:',len(doc['paths']),'API paths;',len(plugin['paths']),'plugin paths')
