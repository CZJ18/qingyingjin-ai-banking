"""Add module8 contracts and synchronize only API plugin artifacts recursively."""
from pathlib import Path
import copy
import yaml

root = Path(__file__).resolve().parents[1]
target = root / 'api/openapi.yaml'
doc = yaml.safe_load(target.read_text(encoding='utf-8'))
s = doc['components']['schemas']
STR = {'type': 'string'}
INT = {'type': 'integer', 'format': 'int64'}
UUID = {'type': 'string', 'format': 'uuid'}
ID = {'type': 'string', 'pattern': '^(0|[1-9][0-9]*)$'}
RAW = {'type': 'object', 'additionalProperties': True}
KINDS = ['transfer','savings_deposit','savings_withdrawal','cancel_subscription','budget_rule_update','account_status','open_account','import_transaction','create_budget','create_savings_goal','subscription_manage']
def ref(n): return {'$ref': '#/components/schemas/' + n}
def arr(v): return {'type': 'array', 'items': v}
def schema(n, props, optional=()):
    s[n] = {'type':'object','additionalProperties':False,'properties':props,'required':[k for k in props if k not in optional]}
schema('ExecutionPrepareInput', {'kind':{'type':'string','enum':KINDS},'parameters':RAW,'expires_in_seconds':dict(INT,minimum=1)}, ('expires_in_seconds',))
schema('ExecutionAuthorizeInput', {'decision':{'type':'string','enum':['approve','reject']},'intent_digest':STR,'confirmation_nonce':STR})
schema('ExecutionExecuteInput', {'authorization_id':UUID,'intent_digest':STR,'expected_version':dict(INT,minimum=1)})
schema('ExecutionGetInput', {'operation_id':UUID})
# Named strict parameter contracts document every kind without a Coze allOf adapter.
parameter_schemas = {
 'transfer':'ExecutionTransferParameters','savings_deposit':'ExecutionSavingsDepositParameters',
 'savings_withdrawal':'ExecutionSavingsWithdrawalParameters','cancel_subscription':'ExecutionCancelSubscriptionParameters',
 'budget_rule_update':'ExecutionBudgetRuleParameters','account_status':'ExecutionAccountStatusParameters',
 'open_account':'ExecutionOpenAccountParameters','import_transaction':'ExecutionImportTransactionParameters',
 'create_budget':'ExecutionCreateBudgetParameters','create_savings_goal':'ExecutionCreateSavingsGoalParameters',
 'subscription_manage':'ExecutionSubscriptionManageParameters'}
for kind,source in [('transfer','TransferInput'),('cancel_subscription','SubscriptionIntentInput'),('open_account','AccountInput'),('import_transaction','TransactionInput'),('create_budget','BudgetInput'),('create_savings_goal','GoalInput')]:
    s[parameter_schemas[kind]]=copy.deepcopy(s[source])
for kind in ['savings_deposit','savings_withdrawal']:
    schema(parameter_schemas[kind],{'goal_id':dict(ID,pattern='^[1-9][0-9]*$'),'input':ref('GoalTransferInput')})
schema('ExecutionAccountStatusParameters',{'account_id':dict(ID,pattern='^[1-9][0-9]*$'),'expected_version':dict(INT,minimum=1),'status':{'type':'string','enum':['active','frozen','inactive','closed']}})
schema('ExecutionBudgetRuleParameters',{'budget_id':dict(ID,pattern='^[1-9][0-9]*$'),'rule_id':dict(ID,pattern='^[1-9][0-9]*$'),'expected_digest':STR,'enabled':{'type':'boolean'}})
schema('ExecutionSubscriptionManageParameters',{'subscription_id':dict(ID,pattern='^[1-9][0-9]*$'),'input':ref('SubscriptionManageInput')})
s['ExecutionPrepareInput']['properties']['parameters']['description']='Strict kind-specific object. Schema mapping: '+', '.join(k+'='+v for k,v in parameter_schemas.items())+'.'

op = {k:STR for k in ['kind','permission','agent','resource_digest','currency','intent_digest','confirmation_nonce','status','authorization_id','risk_level','error_code','trace_id','task_id','audit_id']}
op.update({k:ID for k in ['user_id','resource_id','source_account_id','target_account_id']})
op.update({k:INT for k in ['amount_minor','version','expires_at','created_at','updated_at']})
op.update({k:RAW for k in ['parameters','snapshot','result']})
op['id']=UUID
op.update(skill_id=STR,skill_version=INT)
schema('ExecutionOperation',op)
schema('ExecutionAuthorization',dict(id=UUID,user_id=ID,operation_id=UUID,decision=STR,intent_digest=STR,expires_at=INT,consumed_at=INT,created_at=INT))
schema('ExecutionRiskRule',dict(name=STR,version=INT,enabled={'type':'boolean'},risk_level=STR,limit_minor=INT,description=STR))
schema('ExecutionRiskEvent',dict(id=UUID,user_id=ID,operation_id=UUID,rule_name=STR,risk_level=STR,decision=STR,error_code=STR,evidence=RAW,trace_id=STR,created_at=INT))
audit={k:STR for k in ['agent','operation_id','operation_type','permission','authorization_id','authorization_digest','risk_level','state','error_code','trace_id','task_id']}
audit.update(id=UUID,user_id=ID,created_at=INT)
schema('ExecutionAudit',audit,('user_id',))
schema('ExecutionView',dict(operation=ref('ExecutionOperation'),authorizations=arr(ref('ExecutionAuthorization')),risk_events=arr(ref('ExecutionRiskEvent'))))
for name,data in [('ExecutionView',ref('ExecutionView')),('ExecutionOperationList',arr(ref('ExecutionOperation'))),('ExecutionRiskRuleList',arr(ref('ExecutionRiskRule'))),('ExecutionAuditList',arr(ref('ExecutionAudit')))]:
    envelope=copy.deepcopy(s['Envelope']);envelope['properties']['data']=data;envelope['required']=list(dict.fromkeys(envelope.get('required',[])+['data']));s[name+'Response']=envelope
s['Actor']['properties'].update(token_id=STR,agent_type=STR,task_id=STR)
s['Actor']['properties']['kind']={'type':'string','enum':['user','agent','tool']}
def route(path,method,opid,body=None,result='ExecutionView',status='200',idem=False):
    params=[{'$ref':'#/components/parameters/TraceID'},{'$ref':'#/components/parameters/TaskID'}]
    if '{id}' in path: params.append({'name':'id','in':'path','required':True,'schema':UUID})
    if idem: params.append({'$ref':'#/components/parameters/IdempotencyKey'})
    item={'operationId':opid,'tags':['Execution'],'security':[{'BankingToolKey' if path.startswith('/tools/') else 'UserBearer':[]}],'parameters':params,'description':'Owner-scoped gateway. READ and PREPARE do not execute. Authorize and execute require a signed human user JWT, exact intent digest and one-time confirmation. Execution consumes authorization, validates resource version and risk inside the financial transaction. Audit write failure fails closed. Amounts are integer simulated CNY cents; no external bank connection. Service credential identity is operator-bound, never a model argument.','responses':{}}
    if body: item['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
    for code,name,text in [(status,result+'Response','Success'),('400','Envelope','INVALID_ARGUMENT'),('401','Envelope','UNAUTHENTICATED'),('403','Envelope','FORBIDDEN / GATEWAY_REQUIRED'),('404','Envelope','NOT_FOUND'),('409','Envelope','VERSION_CONFLICT / STATE_CONFLICT / IDEMPOTENCY_CONFLICT'),('422','Envelope','Risk denied / expired / authorization invalid'),('503','Envelope','DEPENDENCY_UNAVAILABLE')]:
        item['responses'][code]={'description':text,'content':{'application/json':{'schema':ref(name)}}}
    doc['paths'].setdefault(path,{})[method]=item
p='/api/v1/execution'
route(p+'/operations','post','prepare_user_operation','ExecutionPrepareInput',status='201',idem=True)
route(p+'/operations','get','list_operations',result='ExecutionOperationList')
doc['paths'][p+'/operations']['get']['parameters'] += [{'name':'kind','in':'query','schema':{'type':'string','enum':KINDS}},{'name':'resource_id','in':'query','schema':ID}]
doc['paths'][p+'/operations']['get']['description']+=' Returns at most 100 operations.'
route(p+'/operations/{id}','get','get_user_operation')
route(p+'/operations/{id}/authorize','post','authorize_operation','ExecutionAuthorizeInput')
route(p+'/operations/{id}/execute','post','execute_operation','ExecutionExecuteInput',idem=True)
route(p+'/operations/{id}/audits','get','get_operation_audits',result='ExecutionAuditList')
route(p+'/risk-rules','get','get_execution_risk_rules',result='ExecutionRiskRuleList')
route('/tools/v1/execution/prepare_operation','post','prepare_operation','ExecutionPrepareInput',idem=True)
route('/tools/v1/execution/get_operation','post','get_operation','ExecutionGetInput')
for path,item in doc['paths'].items():
    if path in ['/api/v1/simulation/accounts','/api/v1/transactions/import','/api/v1/transfers','/api/v1/savings/goals','/api/v1/budgets'] or any(path.endswith(suffix) for suffix in ['/approvals','/deposits','/withdrawals','/status','/confirm','/manage']):
        for method,operation in item.items():
            if method in ['post','patch'] and '/orchestrations/' not in path:
                operation['description']='Legacy financial mutation blocked with GATEWAY_REQUIRED when module8 gateway is configured. Prepare, authorize and execute through /api/v1/execution/operations.'
                operation['responses']['403']={'description':'GATEWAY_REQUIRED','content':{'application/json':{'schema':ref('Envelope')}}}
doc['info']['version']='0.8.0'
doc['tags']=[t for t in doc.get('tags',[]) if t['name']!='Execution']+[{'name':'Execution'}]
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
pt=root/'api/coze-plugin.openapi.yaml'
plugin=yaml.safe_load(pt.read_text(encoding='utf-8'));plugin['info']['version']='0.8.0'
for path,item in doc['paths'].items():
    if path.startswith('/tools/v1/execution/'): plugin['paths'][path]=item
for name,value in s.items():
    if name.startswith('Execution'): plugin['components']['schemas'][name]=value
def include_references(value):
    if isinstance(value,dict):
        reference=value.get('$ref','')
        prefix='#/components/schemas/'
        if reference.startswith(prefix):
            name=reference[len(prefix):]
            if name not in plugin['components']['schemas']:
                plugin['components']['schemas'][name]=copy.deepcopy(s[name]);include_references(s[name])
        for child in value.values(): include_references(child)
    elif isinstance(value,list):
        for child in value: include_references(child)
for name,value in list(plugin['components']['schemas'].items()):
    if name.startswith('Execution'): include_references(value)
serialized=yaml.safe_dump(plugin,allow_unicode=True,sort_keys=False)
pt.write_text(serialized,encoding='utf-8')
# Copy only dedicated OpenAPI plugin artifacts, never agent config YAML.
for artifact in (root.parents[1]/'coze/banking').rglob('*.yaml'):
    if artifact.name in ['plugin.openapi.yaml','coze-plugin.openapi.yaml']: artifact.write_text(serialized,encoding='utf-8')
print('Execution contracts generated:',len(doc['paths']),'API paths;',len(plugin['paths']),'plugin paths')
