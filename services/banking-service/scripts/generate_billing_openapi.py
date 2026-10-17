"""Append module4 API and generate a read-only, identity-bound Coze plugin."""
import copy,json
from pathlib import Path
import yaml
root=Path(__file__).resolve().parents[1]
target=root/'api/openapi.yaml';doc=yaml.safe_load(target.read_text(encoding='utf-8'))
def convert(v):
    if isinstance(v,list):return [convert(x) for x in v]
    if not isinstance(v,dict):return v
    d={k:convert(x) for k,x in v.items() if k not in ('$schema','$id')}
    if 'const' in d:d['enum']=[d.pop('const')]
    if 'enum' in d and 'type' not in d:
        sample=d['enum'][0];d['type']='boolean' if isinstance(sample,bool) else 'integer' if isinstance(sample,int) else 'string'
    if isinstance(d.get('type'),list):types=d['type'];d['type']=next(t for t in types if t!='null');d['nullable']='null' in types
    return d
schemas=doc['components']['schemas'];ref=lambda n:{'$ref':'#/components/schemas/'+n}
report=convert(json.loads((root/'internal/billing/schemas/billing-report.schema.json').read_text(encoding='utf-8')))
report['properties']['accounts']['items']=copy.deepcopy(schemas['BankAccount'])
schemas['BillingReport']=report
ID={'type':'string','pattern':'^[1-9][0-9]*$'}
fields={'from':{'type':'string','format':'date-time'},'to':{'type':'string','format':'date-time'},'timezone':{'type':'string','enum':['UTC','Asia/Shanghai'],'default':'Asia/Shanghai'},'account_id':ID,'limit':{'type':'integer','minimum':0,'maximum':100,'default':100},'offset':{'type':'integer','minimum':0,'maximum':10000,'default':0}}
schemas['BillingInput']={'type':'object','additionalProperties':False,'properties':fields,'required':['from','to']}
schemas['BillingQueryInput']={'type':'object','additionalProperties':False,'properties':{'question':{'type':'string','minLength':1,'maxLength':4000},'timezone':fields['timezone'],'account_id':ID},'required':['question']}
schemas['BillingQueryResult']={'type':'object','properties':{'status':{'type':'string','enum':['awaiting_clarification','analyzed']},'execute_id':{'type':'string'},'missing_information':{'type':'array','items':{'type':'string'}},'report':dict(report,nullable=True)},'required':['status','execute_id','missing_information','report']}
for n in ['BillingReport','BillingQueryResult']:schemas[n+'Response']={'allOf':[ref('Envelope'),{'type':'object','properties':{'data':ref(n)},'required':['data']}]}
names=report['properties']['tool']['enum']
def route(path,operation,security,body=None,method='post',result='BillingReportResponse'):
    params=[{'$ref':'#/components/parameters/TraceID'},{'$ref':'#/components/parameters/TaskID'}]
    if '{id}' in path:params.append({'name':'id','in':'path','required':True,'schema':{'type':'string','format':'uuid'}})
    op={'operationId':operation,'tags':['Billing'],'security':[{security:[]}],'parameters':params,'description':'Read-only financial analysis, amounts in integer CNY cents. External persisted transactions only, including record_only facts. Half-open period <=366 days; at most 10000 loaded records including baseline/full budget months. Pagination applies only to returned transactions, not totals. UTC full-month budgets include all user accounts. Anomalies require review, no confirmed loss or guaranteed savings; no health judgement. Reports/audits persist; no funds change.','responses':{}}
    if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
    for status,desc,name in [('200','Persisted authoritative Go analysis',result),('400','INVALID_ARGUMENT','Envelope'),('401','UNAUTHENTICATED','Envelope'),('403','FORBIDDEN','Envelope'),('404','NOT_FOUND','Envelope'),('422','ANALYSIS_LIMIT / INVALID_TRANSACTION_DATA / MODEL_OUTPUT_INVALID','Envelope'),('503','COZE_UNAVAILABLE / DEPENDENCY_UNAVAILABLE','Envelope')]:op['responses'][status]={'description':desc,'content':{'application/json':{'schema':ref(name)}}}
    doc['paths'].setdefault(path,{})[method]=op
for prefix,security in [('/api/v1','UserBearer'),('/tools/v1','BankingToolKey')]:
    for name in names:route(prefix+'/billing/'+name,('user_' if security=='UserBearer' else '')+name,security,'BillingInput')
    route(prefix+'/billing/reports/{id}',('user_' if security=='UserBearer' else '')+'get_billing_report',security,method='get')
route('/api/v1/billing/query','query_billing','UserBearer','BillingQueryInput',result='BillingQueryResultResponse')
doc['components']['securitySchemes']['BankingToolKey']={'type':'apiKey','in':'header','name':'X-Banking-Tool-Key'}
doc['info']['version']='0.4.0';doc['info']['title']='Qingyingjin Banking, Goals and Billing API'
doc['tags']=[t for t in doc.get('tags',[]) if t['name']!='Billing']+[{'name':'Billing'}]
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
plugin_target=root/'api/coze-plugin.openapi.yaml';old=yaml.safe_load(plugin_target.read_text(encoding='utf-8'))
probe=old['paths']['/tools/v1/integration/context'];probe=copy.deepcopy(probe)
for response in probe['get']['responses'].values():response['content']['application/json']['schema']={'$ref':'#/components/schemas/IntegrationProbeEnvelope'}
probe_schema=old['components']['schemas'].get('IntegrationProbeEnvelope',old['components']['schemas']['Envelope'])
flat_response=copy.deepcopy(schemas['Envelope']);flat_response['properties']['data']=ref('BillingReport');flat_response['required']=list(dict.fromkeys(flat_response['required']+['data']))
# Local Coze exec_tool.go traverses Type/Properties, not allOf. Use explicit
# object schemas and primitive types for the plugin's response contract.
plugin={'openapi':'3.0.3','info':{'title':'Qingyingjin Banking Read-only Billing Tools','version':'0.4.0','description':'Service credential is bound to one Banking user by operator configuration. Never pass user_id or credentials as model arguments. No financial write/approval endpoints.'},'servers':[{'url':'http://banking-service:8090'}],'paths':{'/tools/v1/integration/context':probe},'components':{'securitySchemes':{'BankingToolKey':doc['components']['securitySchemes']['BankingToolKey']},'parameters':doc['components']['parameters'],'schemas':{'IntegrationProbeEnvelope':probe_schema,'Envelope':schemas['Envelope'],'BillingInput':schemas['BillingInput'],'BillingReport':report,'BillingReportResponse':flat_response}}}
for path,item in doc['paths'].items():
    if path.startswith('/tools/v1/billing/'):plugin['paths'][path]=item
plugin_target.write_text(yaml.safe_dump(plugin,allow_unicode=True,sort_keys=False),encoding='utf-8')
print('Billing contracts generated:',len(doc['paths']),'API paths;',len(plugin['paths']),'read-only plugin paths')
