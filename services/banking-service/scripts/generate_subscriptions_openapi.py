"""Append module5 API; plugin exposes proposals only, never confirmation."""
import copy,json
from pathlib import Path
import yaml
root=Path(__file__).resolve().parents[1]
target=root/'api/openapi.yaml';doc=yaml.safe_load(target.read_text(encoding='utf-8'));schemas=doc['components']['schemas']
def convert(v):
 if isinstance(v,list):return [convert(x) for x in v]
 if not isinstance(v,dict):return v
 d={k:convert(x) for k,x in v.items() if k not in ('$schema','$id')}
 if 'const' in d:d['enum']=[d.pop('const')]
 if isinstance(d.get('type'),list):t=d['type'];d['type']=next(x for x in t if x!='null');d['nullable']='null' in t
 return d
for source,name in [('subscription-report','SubscriptionReport'),('subscription-detail','SubscriptionDetail'),('subscription-intent','SubscriptionIntent'),('subscription','Subscription')]:schemas[name]=convert(json.loads((root/('internal/subscription/schemas/'+source+'.schema.json')).read_text(encoding='utf-8')))
ref=lambda n:{'$ref':'#/components/schemas/'+n}
ID={'type':'string','pattern':'^[1-9][0-9]*$'}
def obj(props,required=[]):return {'type':'object','additionalProperties':False,'properties':props,'required':required}
schemas['SubscriptionDetectionInput']=obj({'from':{'type':'string','format':'date-time'},'to':{'type':'string','format':'date-time'},'account_id':ID})
schemas['SubscriptionIDInput']=obj({'subscription_id':ID},['subscription_id'])
schemas['SubscriptionIntentInput']=obj({'subscription_id':ID,'expected_version':{'type':'integer','minimum':1}},['subscription_id','expected_version'])
schemas['SubscriptionManageInput']=obj({'expected_version':{'type':'integer','minimum':1},'confirm_local_simulation':{'type':'boolean'},'last_used_at':{'type':'integer','minimum':1}},['expected_version','confirm_local_simulation'])
schemas['SubscriptionConfirmInput']=obj({'decision':{'type':'string','enum':['approve','reject']},'confirmation_hash':{'type':'string','pattern':'^[a-f0-9]{64}$'}},['decision','confirmation_hash'])
schemas['SubscriptionEmptyInput']=obj({})
for n in ['SubscriptionReport','SubscriptionDetail','SubscriptionIntent','Subscription']:
 flat=copy.deepcopy(schemas['Envelope']);flat['properties']['data']=ref(n);flat['required']=list(dict.fromkeys(flat['required']+['data']));schemas[n+'Response']=flat
names=['detect_subscriptions','get_subscription_details','analyze_subscription_cost','suggest_subscription_actions','create_cancel_intent','get_subscription_status']
def route(path,method,op,body=None,result='SubscriptionReport',status='200',idem=False):
 tool=path.startswith('/tools/v1/');params=[{'$ref':'#/components/parameters/TraceID'},{'$ref':'#/components/parameters/TaskID'}]
 if '{id}' in path:params.append({'name':'id','in':'path','required':True,'schema':ID})
 if idem:params.append({'$ref':'#/components/parameters/IdempotencyKey'})
 operation={'operationId':op,'tags':['Subscription'],'security':[{'BankingToolKey' if tool else 'UserBearer':[]}],'parameters':params,'description':'Local simulated subscription only. Candidate evidence does not prove merchant contract or inactivity. Forecasts are integer CNY cents and never available balance or goal deposits. Cancellation proposal is gateway checked and version bound; execution requires separate user JWT confirmation. Forecast period uses currently linked evidence, not all bank expenses. Detection <=1096 days, <=10000 transactions; reports <=1000 subscriptions/10000 payments. A terminal failed/expired result is a business outcome, inspect status and failure_code.','responses':{}}
 if body:operation['requestBody']={'required':body!='SubscriptionEmptyInput','content':{'application/json':{'schema':ref(body)}}}
 for s,d,n in [(status,'Successful result; inspect business status',result+'Response'),('400','INVALID_ARGUMENT','Envelope'),('401','UNAUTHENTICATED','Envelope'),('403','FORBIDDEN','Envelope'),('404','NOT_FOUND (including unowned resource)','Envelope'),('409','IDEMPOTENCY_CONFLICT / VERSION_CONFLICT / STATE_CONFLICT','Envelope'),('422','APPROVAL_REQUIRED / RISK_REJECTED / ANALYSIS_LIMIT / MODEL_OUTPUT_INVALID','Envelope'),('503','DEPENDENCY_UNAVAILABLE','Envelope')]:operation['responses'][s]={'description':d,'content':{'application/json':{'schema':ref(n)}}}
 doc['paths'].setdefault(path,{})[method]=operation
for prefix in ['/api/v1','/tools/v1']:
 for n in names:
  body='SubscriptionDetectionInput' if n=='detect_subscriptions' else 'SubscriptionIDInput' if n in ['get_subscription_details','get_subscription_status'] else 'SubscriptionIntentInput' if n=='create_cancel_intent' else 'SubscriptionEmptyInput'
  result='SubscriptionDetail' if n in ['get_subscription_details','get_subscription_status'] else 'SubscriptionIntent' if n=='create_cancel_intent' else 'SubscriptionReport'
  route(prefix+'/subscriptions/'+n,'post',('user_' if prefix=='/api/v1' else '')+n,body,result,'201' if n=='create_cancel_intent' and prefix=='/api/v1' else '200',n=='create_cancel_intent')
route('/api/v1/subscriptions','get','list_subscriptions')
route('/api/v1/subscriptions/{id}','get','get_user_subscription',result='SubscriptionDetail')
route('/api/v1/subscriptions/{id}/history','get','get_subscription_history',result='SubscriptionDetail')
route('/api/v1/subscriptions/{id}/manage','post','manage_local_subscription','SubscriptionManageInput','Subscription','200',True)
route('/api/v1/subscriptions/analyses/{id}','get','get_subscription_analysis')
route('/api/v1/subscription-intents/{id}','get','get_cancel_intent',result='SubscriptionIntent')
route('/api/v1/subscription-intents/{id}/confirm','post','confirm_local_subscription_cancel','SubscriptionConfirmInput','SubscriptionIntent')
doc['info']['version']='0.5.0';doc['info']['title']='Qingyingjin Banking Goals Billing and Subscription API'
doc['tags']=[t for t in doc.get('tags',[]) if t['name']!='Subscription']+[{'name':'Subscription'}]
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
pt=root/'api/coze-plugin.openapi.yaml';plugin=yaml.safe_load(pt.read_text(encoding='utf-8'))

plugin['info']['version']='0.5.0';plugin['info']['title']='Qingyingjin Billing and Subscription Tools'
plugin['info']['description']='Operator bound user identity. Read financial evidence and create gateway-checked cancellation proposals only. No user approval, cancellation execution, balance or savings writes.'
for path,item in doc['paths'].items():
 if path.startswith('/tools/v1/subscriptions/'):plugin['paths'][path]=item
for name,schema in schemas.items():
 if name.startswith('Subscription'):plugin['components']['schemas'][name]=schema
pt.write_text(yaml.safe_dump(plugin,allow_unicode=True,sort_keys=False),encoding='utf-8')
print('Subscription contracts generated:',len(doc['paths']),'API paths;',len(plugin['paths']),'plugin paths')
