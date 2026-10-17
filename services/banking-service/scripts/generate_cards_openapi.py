"""Add user-only simulated-card contracts; never export them as Agent tools."""
from pathlib import Path
import yaml

path = Path(__file__).resolve().parents[1] / 'api/openapi.yaml'
doc = yaml.safe_load(path.read_text(encoding='utf-8'))
schemas = doc['components']['schemas']
ref = lambda name: {'$ref': '#/components/schemas/' + name}
string = {'type': 'string'}
integer = {'type': 'integer', 'format': 'int64', 'minimum': 0}
properties = {
    'id': {'type':'string','format':'uuid'}, 'account_id': {'type':'string','pattern':'^[1-9][0-9]*$'},
    'issuer': dict(string,maxLength=96), 'name':dict(string,maxLength=96),
    'last_four': {'type':'string','pattern':'^[0-9]{4}$'},
    'card_type': {'type':'string','enum':['debit','credit']},
    'currency': {'type':'string','enum':['CNY']},
    'status': {'type':'string','enum':['active','unbound']},
    'is_default': {'type':'boolean'}, 'is_simulated': {'type':'boolean','enum':[True]},
    'credit_limit_minor':dict(integer,maximum=1000000000000),
    'credit_used_minor':dict(integer,maximum=1000000000000),
    'credit_available_minor':dict(integer,maximum=1000000000000),
    'created_at':integer,'updated_at':integer,
}
schemas['BankCard']={'type':'object','additionalProperties':False,'properties':properties,'required':list(properties)}
input_properties={k:properties[k] for k in ['account_id','issuer','name','last_four','card_type','credit_limit_minor']}
input_properties.update(action={'type':'string','enum':['bind','unbind','set_default']},card_id={'type':'string','format':'uuid'})
schemas['ExecutionCardManageParameters']={'type':'object','additionalProperties':False,'properties':input_properties,'required':['action'],
 'description':'User-only. bind requires account_id,issuer,name,last_four,card_type; debit limit must be zero. unbind/set_default require only action and card_id. No PAN/CVV, no credit payments or repayments.'}
schemas['ExecutionPrepareInput']['properties']['kind']['enum']=list(dict.fromkeys(schemas['ExecutionPrepareInput']['properties']['kind']['enum']+['card_manage']))
description=schemas['ExecutionPrepareInput']['properties']['parameters'].get('description','')
if 'card_manage=ExecutionCardManageParameters' not in description:
 schemas['ExecutionPrepareInput']['properties']['parameters']['description']=description+' card_manage=ExecutionCardManageParameters (human user only).'
for name,data in [('CardResponse',ref('BankCard')),('CardListResponse',{'type':'array','items':ref('BankCard')})]:
 schemas[name]={'allOf':[ref('Envelope'),{'type':'object','properties':{'data':data},'required':['data']}]}
for endpoint,name,data in [('/api/v1/cards','listBankCards','CardListResponse'),('/api/v1/cards/{id}','getBankCard','CardResponse')]:
 operation={'operationId':name,'tags':['Cards'],'security':[{'UserBearer':[]}],
  'description':'Simulated card metadata owned by the current user. Writes require the trusted card_manage execution gateway. Credit is not cash balance.',
  'responses':{'200':{'description':'success','content':{'application/json':{'schema':ref(data)}}}}}
 for status in ['400','401','403','404','503']:
  operation['responses'][status]={'description':'Error','content':{'application/json':{'schema':ref('Envelope')}}}
 if '{id}' in endpoint:operation['parameters']=[{'name':'id','in':'path','required':True,'schema':{'type':'string','format':'uuid'}}]
 doc['paths'][endpoint]={'get':operation}
doc['info']['version']='0.17.0'
path.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False,width=100),encoding='utf-8')
print('Updated user-only card API: 0.17.0')
