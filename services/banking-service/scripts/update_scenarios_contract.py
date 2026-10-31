"""Extend the existing execution gateway contract; no public Agent write tools."""
from pathlib import Path
import yaml

p=Path(__file__).resolve().parents[1]/'api/openapi.yaml'
d=yaml.safe_load(p.read_text(encoding='utf8'));s=d['components']['schemas']
ref=lambda n:{'$ref':'#/components/schemas/'+n}
integer={'type':'integer','format':'int64','minimum':0,'maximum':1000000000000}
ident={'type':'string','pattern':'^[1-9][0-9]*$'}
text={'type':'string'}
def schema(name,properties,required=None):
 s[name]={'type':'object','additionalProperties':False,'properties':properties,'required':list(properties) if required is None else required}
schema('ExecutionScheduledTransferParameters',{'source_account_id':ident,'target_account_id':ident,'amount_minor':dict(integer,minimum=1),'execute_at':{'type':'integer','format':'int64','description':'Exact future Unix milliseconds within30 days; execution authorization expires15 minutes after this time. No funds post before due.'},'description':dict(text,maxLength=128)})
schema('ExecutionInvestmentAssessmentParameters',{'answers':{'type':'array','minItems':3,'maxItems':3,'items':{'type':'integer','minimum':1,'maximum':3}}})
schema('ExecutionInvestmentOrderParameters',{'action':{'type':'string','enum':['buy','redeem']},'product_id':{'type':'string','enum':['sim-cash-v1','sim-bond-v1','sim-equity-v1']},'account_id':ident,'amount_minor':dict(integer,minimum=1),'assessment_id':dict(text,description='Owned completed assessment required for buying; not used as a substitute for financial authorization.')})
schema('ExecutionOccasionPlanParameters',{'name':dict(text,maxLength=96),'birthday':{'type':'string','format':'date'},'account_id':ident,'reserve_minor':dict(integer,minimum=1),'items':{'type':'array','minItems':1,'maxItems':2,'uniqueItems':True,'items':{'type':'string','enum':['flowers','cake']}}})
schema('ExecutionOccasionOrderParameters',{'plan_id':{'type':'string','format':'uuid'},'execute_at':{'type':'integer','format':'int64','minimum':0,'description':'Zero explicitly means immediate local simulated order. Nonzero must equal funded plan birthday-minus-two-days09:00 Shanghai due time, within30 days.'}})
schema('ExecutionOccasionReleaseParameters',{'plan_id':{'type':'string','format':'uuid'},'account_id':ident})
schema('InvestmentProduct',{'id':text,'name':text,'risk_level':{'type':'integer','minimum':1,'maximum':3},'minimum_minor':integer,'lock_days':{'type':'integer','minimum':0},'description':text,'is_simulated':{'type':'boolean','enum':[True]}})
schema('ScenarioState',{'products':{'type':'array','items':ref('InvestmentProduct')},'positions':{'type':'array','items':ref('BankAccount')},'operations':{'type':'array','maxItems':1000,'items':ref('ExecutionOperation')},'is_simulated':{'type':'boolean','enum':[True]}})
s['ScenarioResponse']={'allOf':[ref('Envelope'),{'type':'object','properties':{'data':ref('ScenarioState')},'required':['data']}]}
kinds=['scheduled_transfer','investment_assessment','investment_order','occasion_plan','occasion_order','occasion_release']
for name in ['ExecutionPrepareInput','ExecutionOperation']:
 prop=s[name]['properties']['kind']
 if 'enum' in prop:prop['enum']=list(dict.fromkeys(prop['enum']+kinds))
params=s['ExecutionPrepareInput']['properties']['parameters']
params['description']=params.get('description','').split(' Scenarios:')[0]+' Scenarios: scheduled_transfer=ExecutionScheduledTransferParameters; investment_assessment=ExecutionInvestmentAssessmentParameters; investment_order=ExecutionInvestmentOrderParameters; occasion_plan=ExecutionOccasionPlanParameters; occasion_order=ExecutionOccasionOrderParameters; occasion_release=ExecutionOccasionReleaseParameters. Human-only simulated execution; existing approval and ledger apply.'
account=s['BankAccount']['properties']['account_type']
if 'enum' in account:account['enum']=list(dict.fromkeys(account['enum']+['investment','occasion']))
def responses(success):
 return {str(n):{'description':'Success' if n==200 else 'Explicit denial','content':{'application/json':{'schema':ref(success if n==200 else 'Envelope')}}} for n in [200,400,401,403,404,409,422,503]}
d['paths']['/api/v1/execution/scenarios']={'get':{'operationId':'getSimulatedScenarios','tags':['Execution'],'security':[{'UserBearer':[]}],'description':'Human-owned simulated catalog, principal holdings and exact persisted operations. No model request or real bank/merchant integration.','responses':responses('ScenarioResponse')}}
d['paths']['/api/v1/execution/operations/{id}/cancel']={'post':{'operationId':'cancelScheduledSimulation','tags':['Execution'],'security':[{'UserBearer':[]}],'parameters':[{'name':'id','in':'path','required':True,'schema':{'type':'string','format':'uuid'}}],'requestBody':{'required':True,'content':{'application/json':{'schema':ref('ExecutionAuthorizeInput')}}},'description':'Cancel an owned already-authorized schedule. decision must be reject, digest and nonce must match. Cannot undo a completed posting or mutate fee state.','responses':responses('ExecutionViewResponse')}}
# Resolve names against the existing contract instead of inventing parallel DTOs.
for name in ['ExecutionOperation','BankAccount','ExecutionAuthorizeInput','ExecutionViewResponse']:
 assert name in s,name
d['info']['version']='0.23.0'
p.write_text(yaml.safe_dump(d,allow_unicode=True,sort_keys=False,width=100),encoding='utf8')
print('Extended existing user-only execution API with six simulated scenario kinds.')
