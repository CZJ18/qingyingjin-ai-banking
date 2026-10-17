"""P2 schemas and contracts. Existing v1 goal schemas are unchanged."""
from pathlib import Path
import copy
import json
import yaml

root=Path(__file__).resolve().parents[1]
STR={'type':'string'}
INT={'type':'integer','format':'int64'}
ID={'type':'string','pattern':'^[1-9][0-9]*$'}
ZEROID={'type':'string','pattern':'^(0|[1-9][0-9]*)$'}
def obj(p):return {'type':'object','additionalProperties':False,'properties':p,'required':list(p)}
def arr(x):return {'type':'array','items':x}
def ref(n):return {'$ref':'#/components/schemas/'+n}
minor=dict(INT,minimum=0,maximum=1000000000000)
spec=obj({'schema_version':{'type':'string','enum':['2.0']},'currency':{'type':'string','enum':['CNY']},'period_months':dict(INT,minimum=1,maximum=120),'expense_reduction_monthly_minor':minor,'savings_monthly_minor':minor,'health_background':dict(STR,maxLength=2000),'adjustable_categories':dict(arr(dict(STR,minLength=1,maxLength=64)),maxItems=32,uniqueItems=True),'protected_categories':dict(arr(dict(STR,minLength=1,maxLength=64)),maxItems=32,uniqueItems=True),'prohibited_operations':dict(arr({'type':'string','enum':['transfer','cancel_subscription','reduce_essential_expenses','all_financial_operations','weight_based_incentives']}),maxItems=32,uniqueItems=True),'evidence':{'type':'object','maxProperties':100,'additionalProperties':dict(STR,maxLength=10000)}})
schema=copy.deepcopy(spec);schema.update({'$schema':'https://json-schema.org/draft/2020-12/schema','$id':'urn:qingrongjin:goal:v2'})
(root/'internal/lifestyle/goal-v2.schema.json').write_text(json.dumps(schema,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
doc=yaml.safe_load((root/'api/openapi.yaml').read_text(encoding='utf-8'));s=doc['components']['schemas']
s['LifestyleSpec']=spec
s['LifestyleGoalInput']=obj({'text':dict(STR,minLength=1,maxLength=10000),'spec':ref('LifestyleSpec'),'expected_version':dict(INT,minimum=0)})
s['LifestyleCompileInput']=obj({'expected_version':dict(INT,minimum=1),'budget_id':ZEROID,'billing_report_id':dict(STR,maxLength=64),'financial_plan_id':ZEROID})
s['LifestyleCompileToolInput']=obj(dict(s['LifestyleCompileInput']['properties'],goal_id=ID))
s['LifestyleDecisionInput']=obj({'expected_version':dict(INT,minimum=1),'decision':{'type':'string','enum':['accept','reject']},'plan_digest':dict(STR,minLength=64,maxLength=64)})
s['LifestyleParseInput']=obj({'text':dict(STR,minLength=1,maxLength=10000)})
s['LifestyleIDInput']=obj({'goal_id':ZEROID,'plan_id':ZEROID})
base={'id':ID,'user_id':ID,'created_at':INT,'updated_at':INT}
s['LifestyleGoal']=obj(dict(base,current_version=INT))
s['LifestyleGoalVersion']=obj(dict(base,goal_id=ID,version=INT,text=STR,spec=ref('LifestyleSpec'),trace_id=STR))
s['LifestyleGoalView']=obj({'goal':ref('LifestyleGoal'),'versions':arr(ref('LifestyleGoalVersion'))})
s['LifestyleAllocation']=obj({'historical_minor':minor,'category':STR,'original_minor':minor,'proposed_minor':minor,'protected':{'type':'boolean'}})
s['LifestyleOption']=obj({'name':{'type':'string','enum':['conservative','balanced','active']},'budget_minor':minor,'expense_reduction_minor':minor,'savings_monthly_minor':minor,'categories':arr(ref('LifestyleAllocation')),'explanation':STR})
s['LifestyleResult']=obj({'schema_version':STR,'rule_version':STR,'status':{'type':'string','enum':['ready','conflict','needs_information']},'options':arr(ref('LifestyleOption')),'missing_information':arr(STR),'conflicts':arr(STR),'reduction_gap_minor':minor,'savings_gap_minor':minor,'requires_confirmation':{'type':'boolean'},'financial_execution_enabled':{'type':'boolean','enum':[False]}})
s['LifestyleCategory']=obj({'category':STR,'allocated_minor':minor,'essential_reserve_minor':minor,'observed_minor':minor})
s['LifestyleSnapshot']=obj({'goal':ref('LifestyleSpec'),'budget_minor':minor,'categories':arr(ref('LifestyleCategory')),'complete_month':{'type':'boolean'},'savings_ceiling_minor':dict(INT,nullable=True),'evidence_transaction_ids':arr(STR),'sources':ref('LifestyleCompileInput'),'source_digest':STR})
s['LifestylePlan']=obj(dict(base,goal_id=ID,goal_version=INT,input_digest=STR,snapshot=ref('LifestyleSnapshot'),result=ref('LifestyleResult'),expires_at=INT,audit_id=STR,trace_id=STR))
s['LifestyleRecommendation']=obj(dict(base,goal_id=ID,plan_id=ID,type=STR,expected_impact_minor=minor,impact_period=STR,explanation=STR,confidence=STR,status={'type':'string','enum':['pending','accepted','rejected','superseded']},version=INT,expires_at=INT,rule_version=STR,option=ref('LifestyleOption')))
s['LifestylePlanView']=obj({'plan':ref('LifestylePlan'),'recommendations':arr(ref('LifestyleRecommendation'))})
s['LifestyleDecision']=obj(dict(base,recommendation_id=ID,decision=STR,audit_id=STR,trace_id=STR))
s['LifestyleParseResult']=obj({'status':{'type':'string','enum':['parsed','needs_information']},'spec':ref('LifestyleSpec'),'missing_information':arr({'type':'string','enum':['period_months','goal_details']}),'execute_id':STR,'workflow_id':ID})
s['LifestyleParseResult']['required'].remove('spec')
s['LifestyleParseResult']['description']='parsed includes independently validated spec; needs_information omits spec and never creates a financial task. execute_id and workflow_id identify the real native run.'
s['OrchestrationBindings']['properties'].update({k:STR for k in ['lifestyle_goal_id','lifestyle_plan_id','budget_id','billing_report_id']})
s['OrchestrationBindings']['properties']['lifestyle_goal_version']=dict(INT,minimum=1)
s['OrchestrationAgentOutput']['properties']['explanation_evidence_ids']=dict(arr(STR),uniqueItems=True)
s['OrchestrationAgentOutput']['properties']['explanation_finding_ids']=dict(arr(STR),uniqueItems=True)
def envelope(name,data):
 e=copy.deepcopy(s['Envelope']);e['properties']['data']=data;e['required']=list(dict.fromkeys(e.get('required',[])+['data']));s[name]=e;return name
def operation(method,path,name,inputname,data,status=200,plugin=False,key=False):
 response=envelope(name+'Response',data)
 op={'operationId':name,'tags':['Lifestyle'],'security':[{'ToolKey' if plugin else 'UserBearer':[]}],'description':'Suggestions only. Accepting a recommendation never authorizes execution. Missing data stays explicit.','responses':{str(status):{'description':'Success','content':{'application/json':{'schema':ref(response)}}}}}
 for code in [400,401,403,404,409,422,503]:op['responses'][str(code)]={'description':'INVALID_ARGUMENT / UNAUTHENTICATED / FORBIDDEN / NOT_FOUND / VERSION_CONFLICT / STATE_CONFLICT / MODEL_OUTPUT_INVALID / COZE_UNAVAILABLE','content':{'application/json':{'schema':ref('Envelope')}}}
 params=[]
 if '{id}' in path:params.append({'name':'id','in':'path','required':True,'schema':ID})
 if key:params.append({'name':'Idempotency-Key','in':'header','required':True,'schema':dict(STR,minLength=1,maxLength=128)})
 if params:op['parameters']=params
 if inputname:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(inputname)}}}
 doc['paths'].setdefault(path,{})[method]=op

operation('post','/api/v2/lifestyle-goals/parse','parse_lifestyle_goal','LifestyleParseInput',ref('LifestyleParseResult'))
operation('post','/api/v2/lifestyle-goals','create_lifestyle_goal','LifestyleGoalInput',ref('LifestyleGoalView'),201,key=True)
operation('get','/api/v2/lifestyle-goals/{id}','get_lifestyle_goal_v2',None,ref('LifestyleGoalView'))
operation('put','/api/v2/lifestyle-goals/{id}','update_lifestyle_goal','LifestyleGoalInput',ref('LifestyleGoalView'),key=True)
operation('post','/api/v2/lifestyle-goals/{id}/compile','compile_lifestyle_goal','LifestyleCompileInput',ref('LifestylePlanView'),201,key=True)
operation('get','/api/v2/lifestyle-goals/{id}/plans','list_lifestyle_plans',None,arr(ref('LifestylePlanView')))
operation('get','/api/v2/lifestyle-plans/{id}','get_lifestyle_plan_v2',None,ref('LifestylePlanView'))
operation('post','/api/v2/recommendations/{id}/decisions','decide_lifestyle_recommendation','LifestyleDecisionInput',ref('LifestyleDecision'),key=True)
operation('get','/api/v2/recommendations/{id}/decisions','get_lifestyle_decisions',None,arr(ref('LifestyleDecision')))
for path in ['/api/v2/lifestyle-goals/{id}','/api/v2/lifestyle-goals/{id}/plans']:
 doc['paths'][path]['get']['parameters'] += [{'name':'limit','in':'query','schema':dict(INT,minimum=1,maximum=100,default=100)},{'name':'offset','in':'query','schema':dict(INT,minimum=0,maximum=1000000,default=0)}]
for tool,data in [('get_lifestyle_goal','LifestyleGoalView'),('get_lifestyle_plan','LifestylePlanView')]:operation('post','/tools/v1/lifestyle/'+tool,tool,'LifestyleIDInput',ref(data),plugin=True)
operation('post','/tools/v1/lifestyle/compile_lifestyle_plan','compile_lifestyle_plan','LifestyleCompileToolInput',ref('LifestylePlanView'),plugin=True,key=True)
for name in ['OrchestrationTaskSpec','AgentTask','AgentOutput']:
 if name in s:
  role=s[name].get('properties',{}).get('agent_type',{}).get('enum',[])
  if role and 'lifestyle' not in role:role.append('lifestyle')
doc['info']['version']='0.11.0'
(root/'api/openapi.yaml').write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
plugin=yaml.safe_load((root/'api/coze-plugin.openapi.yaml').read_text(encoding='utf-8'))
def expand(node):
 if isinstance(node,list):return [expand(v) for v in node]
 if not isinstance(node,dict):return node
 if '$ref' in node:return expand(s[node['$ref'].split('/')[-1]])
 return {k:expand(v) for k,v in node.items()}
for name,value in s.items():
 if name.startswith('Lifestyle') or name.startswith('get_lifestyle_') or name=='compile_lifestyle_planResponse':
  plugin['components']['schemas'][name]=expand(value)
for path,item in doc['paths'].items():
 if path.startswith('/tools/v1/lifestyle/'):plugin['paths'][path]=copy.deepcopy(item)
plugin['info']['version']='0.11.0'
(root/'api/coze-plugin.openapi.yaml').write_text(yaml.safe_dump(plugin,allow_unicode=True,sort_keys=False),encoding='utf-8')

# Local references only; existing published workflows are not silently replaced.
for path in [root.parents[1]/'coze/banking/supervisor-agent/task-plan.schema.json',root.parents[1]/'coze/banking/supervisor-agent/explanation.schema.json']:
 value=json.loads(path.read_text(encoding='utf-8'))
 def add_role(node):
  if isinstance(node,dict):
   role=node.get('properties',{}).get('agent_type',{}).get('enum',[])
   if role:
    broad=all(r in role for r in ['billing','subscription','savings'])
    if 'lifestyle' in role and not broad:role.remove('lifestyle')
    if broad and 'lifestyle' not in role:role.append('lifestyle')
   for child in node.values():add_role(child)
  elif isinstance(node,list):
   for child in node:add_role(child)
 add_role(value)
 if path.name=='task-plan.schema.json':
  item=value['properties']['tasks']['items'];p=item['properties']
  for field,extra in [('skill_id',['lifestyle_planning']),('tool',['get_lifestyle_goal','get_lifestyle_plan','compile_lifestyle_plan'])]:
   for v in extra:
    if v not in p[field]['enum']:p[field]['enum'].append(v)
  rules=item['allOf']
  rules[:]=[r for r in rules if r.get('if',{}).get('properties',{}).get('skill_id',{}).get('const')!='lifestyle_planning']
  rules.append({'if':{'required':['skill_id'],'properties':{'skill_id':{'const':'lifestyle_planning'}}},'then':{'properties':{'agent_type':{'const':'lifestyle'},'tool':{'enum':['get_lifestyle_goal','get_lifestyle_plan','compile_lifestyle_plan']},'execution_mode':{'enum':['read_only','advice']}}}})
  for tool,mode in [('get_lifestyle_goal','read_only'),('get_lifestyle_plan','read_only'),('compile_lifestyle_plan','advice')]:
   rules[:]=[r for r in rules if r.get('if',{}).get('properties',{}).get('tool',{}).get('const')!=tool]
   rules.append({'if':{'required':['tool'],'properties':{'tool':{'const':tool}}},'then':{'properties':{'agent_type':{'const':'lifestyle'},'execution_mode':{'const':mode}}}})
 path.write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
