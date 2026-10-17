"""Regenerate the module2 contract, retaining module1 foundation paths.
Run from any directory: python services/banking-service/scripts/generate_core_openapi.py
Requires PyYAML only for this documentation maintenance command (not runtime).
"""
from pathlib import Path
import yaml

target = Path(__file__).resolve().parents[1] / 'api/openapi.yaml'
doc = yaml.safe_load(target.read_text(encoding='utf-8'))
doc['info'] = {'title': 'Qingyingjin Banking Core API', 'version': '0.2.0',
 'description': 'Simulated CNY accounts only. Amounts are integer minor units; IDs are decimal strings; timestamps are Unix milliseconds. User JWT is mandatory for Core. Financial changes require explicit user confirmation and a balanced persisted ledger. Module8 may replace the simulation safety gateway. No Agent financial execution endpoints.'}
doc['tags'] = [{'name': n} for n in ['Foundation', 'Users', 'Accounts', 'Transactions', 'Transfers', 'Budgets', 'Savings', 'Audits']]
basepaths = ['/healthz','/readyz','/api/v1/context','/tools/v1/integration/context','/openapi.yaml','/coze-plugin.openapi.yaml']
doc['paths'] = {p: doc['paths'][p] for p in basepaths}
schemas = doc['components']['schemas']
for name in list(schemas):
 if name not in ['Actor', 'Envelope']: del schemas[name]
schemas['Envelope']['properties']['data'] = {}
ref = lambda n: {'$ref': '#/components/schemas/' + n}
arr = lambda n: {'type': 'array', 'items': ref(n)}
ID = {'type': 'string', 'pattern': '^[1-9][0-9]*$', 'description': 'Canonical positive int64 decimal string'}
integer = {'type': 'integer', 'format': 'int64'}
money = dict(integer, minimum=0, maximum=1000000000000, description='Integer CNY cents (never float)')
positive = dict(money, minimum=1)
timestamp = dict(integer, minimum=1, description='Unix milliseconds UTC')
string = {'type': 'string'}
boolean = {'type': 'boolean'}
currency = {'type': 'string', 'enum': ['CNY'], 'default': 'CNY'}
base = {'id': ID, 'user_id': ID, 'created_at': integer, 'updated_at': integer}
def model(name, fields, required=None, strict=False):
 schemas[name] = {'type': 'object', 'properties': fields, 'required': list(fields) if required is None else required}
 if strict: schemas[name]['additionalProperties'] = False

model('User', {'id': ID, 'display_name': string, 'status': string, 'created_at': integer, 'updated_at': integer})
account = dict(base, display_name=string, account_type={'type':'string','enum':['checking','savings']}, masked_account_number=string,
 currency=currency, balance_minor=money, status={'type':'string','enum':['active','frozen','inactive','closed']},version=integer,is_simulated=boolean)
model('BankAccount',account)
schemas['BankAccount']['properties']['savings_goal_id']=ID
model('AccountBalance',dict(account_id=ID,currency=currency,balance_minor=money,ledger_balance_minor=integer,version=integer,reconciled=boolean))
transaction=dict(base,bank_account_id=ID,direction={'type':'string','enum':['income','expense']},amount_minor=positive,currency=currency,category=string,merchant_name=string,description=string,occurred_at=timestamp,kind={'type':'string','enum':['opening','external','internal_transfer']},effect_mode={'type':'string','enum':['record_only','apply_simulated_effect']},trace_id=string,audit_id=string)
model('Transaction',transaction)
schemas['Transaction']['properties'].update(external_transaction_id=string,journal_id=string,transfer_id=ID,task_id=string)
model('TransactionPage',{'items':arr('Transaction'),'total':integer,'limit':{'type':'integer'},'offset':{'type':'integer'}})
model('LedgerEntry',dict(id=ID,user_id=ID,journal_id=string,sequence={'type':'integer','enum':[1,2]},currency=currency,delta_minor=integer,side={'type':'string','enum':['debit','credit']},created_at=integer))
schemas['LedgerEntry']['properties'].update(account_id=ID,transaction_id=ID)
model('Audit',dict(id=string,user_id=ID,action=string,resource_id={'type':'string','pattern':'^[0-9]+$'},outcome=string,trace_id=string,created_at=integer))
schemas['Audit']['properties'].update(task_id=string,error_code=string)
transfer=dict(base,source_account_id=ID,target_account_id=ID,amount_minor=positive,currency=currency,description=string,purpose={'type':'string','enum':['transfer','savings_deposit','savings_withdrawal']},status={'type':'string','enum':['pending_approval','succeeded','failed','rejected']},approval_hash={'type':'string','pattern':'^[a-f0-9]{64}$'},expires_at=timestamp,audit_id=string,trace_id=string)
model('TransferIntent',transfer)
schemas['TransferIntent']['properties'].update(savings_goal_id=ID,journal_id=string,failure_code=string,task_id=string)
model('BudgetCategory',dict(id=ID,user_id=ID,budget_id=ID,category=string,allocated_minor=money,essential_reserve_minor=money))
model('BudgetRule',dict(id=ID,user_id=ID,budget_id=ID,kind={'type':'string','enum':['enforce_reserve']},enabled=boolean))
model('Budget',dict(base,month={'type':'string','pattern':'^[0-9]{4}-[0-9]{2}$'},currency=currency,total_minor=positive,categories=arr('BudgetCategory'),rules=arr('BudgetRule')),list(base)+['month','currency','total_minor'])
schemas['Budget']['properties']['categories']['nullable']=True
schemas['Budget']['properties']['rules']['nullable']=True
model('CategoryUsage',dict(category=string,allocated_minor=money,spent_minor=integer,remaining_minor=integer,essential_reserve_remaining_minor=money))
model('BudgetUsage',dict(budget=ref('Budget'),spent_minor=integer,remaining_minor=integer,essential_reserve_remaining_minor=integer,available_after_reserve_minor=integer,categories=arr('CategoryUsage'),unallocated_category_spend_minor=integer))
model('SavingsPlan',dict(id=ID,user_id=ID,goal_id=ID,monthly_minor=positive,cadence={'type':'string','enum':['monthly']},status={'type':'string','enum':['manual']}))
model('SavingsGoal',dict(base,name=string,target_minor=positive,currency=currency,starts_at=timestamp,ends_at=timestamp,account_id=ID,status=string,plan=ref('SavingsPlan'),current_minor=money,progress_basis_points=integer))
model('SavingsLedger',dict(id=ID,user_id=ID,goal_id=ID,transfer_id=ID,journal_id=string,delta_minor=integer,currency=currency,created_at=integer))
model('UserInput',{'display_name':dict(string,minLength=1,maxLength=128)},strict=True)
model('AccountInput',{'display_name':dict(string,minLength=1,maxLength=128),'account_type':{'type':'string','enum':['checking','savings'],'default':'checking'},'currency':currency,'initial_balance_minor':money,'confirm_simulated_funds':boolean},['display_name'],True)
model('StatusInput',{'status':account['status']},strict=True)
model('TransactionInput',dict(bank_account_id=ID,external_transaction_id=dict(string,minLength=1,maxLength=128),direction=transaction['direction'],amount_minor=positive,currency=currency,category=dict(string,minLength=1,maxLength=64),merchant_name=dict(string,maxLength=128),description=dict(string,maxLength=512),occurred_at=timestamp,effect_mode=transaction['effect_mode'],confirm_simulated_effect=boolean),['bank_account_id','external_transaction_id','direction','amount_minor','category','occurred_at','effect_mode'],True)
model('TransferInput',dict(source_account_id=ID,target_account_id=ID,amount_minor=positive,currency=currency,description=dict(string,maxLength=512)),['source_account_id','target_account_id','amount_minor'],True)
model('ApprovalInput',dict(decision={'type':'string','enum':['approve','reject']},approval_hash=transfer['approval_hash']),strict=True)
model('CategoryInput',dict(category=dict(string,minLength=1,maxLength=64),allocated_minor=money,essential_reserve_minor=money),['category','allocated_minor'],True)
model('BudgetInput',dict(month=schemas['Budget']['properties']['month'],currency=currency,total_minor=positive,categories=dict(arr('CategoryInput'),minItems=1,maxItems=100),enforce_reserve=boolean),['month','total_minor','categories'],True)
model('GoalInput',dict(name=dict(string,minLength=1,maxLength=128),target_minor=positive,currency=currency,starts_at=timestamp,ends_at=timestamp,monthly_minor=positive),['name','target_minor','starts_at','ends_at','monthly_minor'],True)
model('GoalTransferInput',dict(bank_account_id=ID,amount_minor=positive,description=dict(string,maxLength=512)),['bank_account_id','amount_minor'],True)
model('AccountStatus',{'account_id':ID,'status':account['status']})
def envelope(name,data):
 schemas[name]={'allOf':[ref('Envelope'),{'type':'object','required':['data'],'properties':{'data':data}}]}
for name in ['User','BankAccount','AccountBalance','Transaction','TransactionPage','TransferIntent','Budget','BudgetUsage','SavingsGoal','AccountStatus']:
 envelope(name+'Response',ref(name))
for name in ['BankAccount','LedgerEntry','Budget','SavingsGoal','SavingsLedger','Audit']:
 envelope(name+'ListResponse',arr(name))
doc['components']['parameters']['IdempotencyKey']={'name':'Idempotency-Key','in':'header','required':True,'schema':{'type':'string','minLength':1,'maxLength':128,'pattern':'^[!-~]+$'},'description':'Scoped to user/action. Same key + canonical body returns persisted original result; different body returns 409.'}
idparam={'name':'id','in':'path','required':True,'schema':ID}
paramref=lambda n:{'$ref':'#/components/parameters/'+n}
pagination=[{'name':'limit','in':'query','schema':{'type':'integer','minimum':0,'maximum':1000000,'default':50},'description':'Capped at 100'},{'name':'offset','in':'query','schema':{'type':'integer','minimum':0,'maximum':1000000,'default':0}}]
def route(path,method,operation,tag,result,input=None,creation=False,params=None,description=''):
 op={'operationId':operation,'tags':[tag],'security':[{'UserBearer':[]}], 'parameters':[paramref('TraceID'),paramref('TaskID')],'responses':{}}
 if '{id}' in path: op['parameters'].append(idparam)
 if creation: op['parameters'].append(paramref('IdempotencyKey'))
 if params: op['parameters'].extend(params)
 if input: op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(input)}}}
 op['description']=description
 status='201' if creation or operation=='registerUser' else '200'
 op['responses'][status]={'description':'Persisted simulation result','content':{'application/json':{'schema':ref(result)}}}
 for code,desc in [('400','INVALID_ARGUMENT'),('401','UNAUTHENTICATED'),('403','FORBIDDEN'),('404','NOT_FOUND'),('409','IDEMPOTENCY_CONFLICT / VERSION_CONFLICT'),('422','INSUFFICIENT_FUNDS / ACCOUNT_FROZEN / ACCOUNT_CLOSED / APPROVAL_REQUIRED / APPROVAL_EXPIRED / RISK_REJECTED / AMOUNT_LIMIT'),('503','DEPENDENCY_UNAVAILABLE')]:
  op['responses'][code]={'description':desc,'content':{'application/json':{'schema':ref('Envelope')}}}
 doc['paths'].setdefault(path,{})[method]=op
p='/api/v1'
route(p+'/users/me','post','registerUser','Users','UserResponse','UserInput',description='Registers only the validated JWT subject. Replay preserves the existing profile; cannot supply user_id.')
route(p+'/users/me','get','getUser','Users','UserResponse')
route(p+'/simulation/accounts','post','createSimulationAccount','Accounts','BankAccountResponse','AccountInput',True,description='Positive opening funds require confirm_simulated_funds=true and generate a balanced clearing journal. No direct balance setter.')
route(p+'/accounts','get','listAccounts','Accounts','BankAccountListResponse',params=pagination)
route(p+'/accounts/{id}','get','getAccount','Accounts','BankAccountResponse')
route(p+'/accounts/{id}/balance','get','getAccountBalance','Accounts','AccountBalanceResponse',description='Locks the account to read a consistent balance and sum its ledger entries.')
route(p+'/accounts/{id}/ledger','get','getAccountLedger','Accounts','LedgerEntryListResponse',params=pagination)
route(p+'/accounts/{id}/status','patch','updateAccountStatus','Accounts','AccountStatusResponse','StatusInput',description='Simulation user status control. Cannot update currency, funds or ownership; goal account state is protected.')
route(p+'/transactions/import','post','importTransaction','Transactions','TransactionResponse','TransactionInput',True,description='record_only persists a historical fact without financial effect. apply_simulated_effect requires confirm_simulated_effect=true and writes a balanced journal. Both external IDs and idempotency keys are unique.')
filters=[{'name':name,'in':'query','schema':schema} for name,schema in [('bank_account_id',ID),('direction',transaction['direction']),('category',string),('merchant',string),('from',dict(integer,minimum=0)),('to',dict(integer,minimum=0))]]
route(p+'/transactions','get','listTransactions','Transactions','TransactionPageResponse',params=pagination+filters,description='Half-open UTC interval [from,to), exact merchant/category; ordered occurred_at DESC,id DESC.')
route(p+'/transactions/{id}','get','getTransaction','Transactions','TransactionResponse')
route(p+'/transfers','post','proposeTransfer','Transfers','TransferIntentResponse','TransferInput',True,description='Creates immutable pending_approval intent between the same user\'s regular simulated accounts. Does not move money; use goal endpoints for goal accounts.')
route(p+'/transfers/{id}','get','getTransfer','Transfers','TransferIntentResponse')
route(p+'/transfers/{id}/approvals','post','approveAndExecuteSimulationTransfer','Transfers','TransferIntentResponse','ApprovalInput',description='Explicit user approve/reject of exact approval_hash. Approve executes synchronously through safety gateway, locks and ledger in one MySQL transaction. Terminal replay returns previous outcome; a different decision conflicts. Business failure returns 422 with persisted failed intent in data. Database failure rolls back and leaves intent retryable. Intent expires 15 minutes after creation.')
route(p+'/budgets','post','createBudget','Budgets','BudgetResponse','BudgetInput',True,description='Unique UTC month/CNY per user. Category allocation sum cannot exceed total; reserve cannot exceed category allocation. Reserve is a planning constraint, not a bank hold.')
route(p+'/budgets','get','listBudgets','Budgets','BudgetListResponse',params=pagination+[{'name':'month','in':'query','schema':schemas['Budget']['properties']['month']}])
route(p+'/budgets/{id}','get','getBudget','Budgets','BudgetResponse')
route(p+'/budgets/{id}/usage','get','getBudgetUsage','Budgets','BudgetUsageResponse',description='Includes external expense facts for the month, including record_only; excludes openings and internal transfers. Remaining and available may be negative. Category reserve remaining=max(reserve-spent,0); enforce_reserve subtracts it from remaining.')
route(p+'/savings/goals','post','createSavingsGoal','Savings','SavingsGoalResponse','GoalInput',True,description='Creates dedicated zero-funded account and manual monthly plan. Supports regular meals, reasonable exercise and sustainable habits; no weight/diet reward conditions and no auto-debit.')
route(p+'/savings/goals','get','listSavingsGoals','Savings','SavingsGoalListResponse',params=pagination)
route(p+'/savings/goals/{id}','get','getSavingsGoal','Savings','SavingsGoalResponse',description='Progress basis points=current_minor*10000/target_minor; may exceed 10000. Actual funding comes only from authorized goal transfers.')
route(p+'/savings/goals/{id}/ledger','get','getSavingsLedger','Savings','SavingsLedgerListResponse',params=pagination)
for name in ['deposits','withdrawals']:
 route(p+'/savings/goals/{id}/'+name,'post','proposeSavings'+name.title(),'Savings','TransferIntentResponse','GoalTransferInput',True,description='Creates a pending intent only; explicitly approve via /transfers/{id}/approvals. Savings ledger and account journal commit together.')
route(p+'/audits','get','listAudits','Audits','AuditListResponse',params=pagination)
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
print('Generated OpenAPI:', len(doc['paths']), 'paths')

# Keep the module3 contract when regenerating module2 documentation.
import runpy
runpy.run_path(str(Path(__file__).with_name("generate_goals_openapi.py")))
runpy.run_path(str(Path(__file__).with_name("generate_billing_openapi.py")))

runpy.run_path(str(Path(__file__).with_name("generate_subscriptions_openapi.py")))
runpy.run_path(str(Path(__file__).with_name("generate_planning_openapi.py")))
runpy.run_path(str(Path(__file__).with_name("generate_orchestrator_openapi.py")))
runpy.run_path(str(Path(__file__).with_name("generate_execution_openapi.py")))

runpy.run_path(str(Path(__file__).with_name("generate_skills_openapi.py")))
runpy.run_path(str(Path(__file__).with_name("generate_lifestyle_openapi.py")))
