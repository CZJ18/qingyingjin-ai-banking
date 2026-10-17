"""Append the module3 contract to module2. Python/PyYAML are doc tooling only."""
import json
from pathlib import Path
import yaml

root = Path(__file__).resolve().parents[1]
target = root / 'api/openapi.yaml'
doc = yaml.safe_load(target.read_text(encoding='utf-8'))
schemas = doc['components']['schemas']
ref = lambda n: {'$ref': '#/components/schemas/' + n}
ID = {'type': 'string', 'pattern': '^[1-9][0-9]*$'}
integer = {'type': 'integer', 'format': 'int64'}
string = {'type': 'string'}

def convert(value):
    if isinstance(value, list): return [convert(v) for v in value]
    if not isinstance(value, dict): return value
    out = {k: convert(v) for k, v in value.items() if k not in ('$schema', '$id', 'if', 'then', 'allOf')}
    if 'const' in out: out['enum'] = [out.pop('const')]
    if isinstance(out.get('type'), list):
        types = out['type']; out['type'] = next(t for t in types if t != 'null'); out['nullable'] = 'null' in types
    if out.get('type') == 'string': out.pop('maxProperties', None)
    return out

parsed = convert(json.loads((root/'internal/goal/schemas/goal-parse.schema.json').read_text(encoding='utf-8')))
plan = convert(json.loads((root/'internal/goal/schemas/task-plan.schema.json').read_text(encoding='utf-8')))
schemas['ParsedLifeGoal'] = parsed
schemas['CompiledTaskPlan'] = plan
context = plan['properties']['financial_context']
schemas['GoalFinancialContext'] = context
def model(name, fields, required=None, strict=False):
    schemas[name] = {'type':'object','properties':fields,'required':list(fields) if required is None else required}
    if strict: schemas[name]['additionalProperties'] = False
base={'id':ID,'user_id':ID,'created_at':integer,'updated_at':integer}
model('LifeGoal',dict(base,current_version=integer,status=string))
nullable_parsed=dict(parsed,nullable=True)
model('LifeGoalVersion',dict(base,skill_id=string,skill_version=integer,goal_id=ID,version=integer,text=string,financial_context=ref('GoalFinancialContext'),parsed=nullable_parsed,parse_status={'type':'string','enum':['unparsed','parsing','failed','parsed']},parse_id=string,lease_until=integer,workflow_id=string,execute_id=string,error_code=string,trace_id=string,audit_id=string))
model('StoredTaskPlan',dict(base,goal_id=ID,goal_version_id=ID,version=integer,status=string,plan=ref('CompiledTaskPlan'),trace_id=string,audit_id=string))
nullable_plan=dict(schemas['StoredTaskPlan'],nullable=True)
model('LifeGoalView',{'goal':ref('LifeGoal'),'version':ref('LifeGoalVersion'),'task_plan':nullable_plan})
model('LifeGoalInput',{'text':{'type':'string','minLength':1,'maxLength':8000},'financial_context':dict(context,required=[]),'expected_version':dict(integer,minimum=1)},['text'],True)
model('LifeGoalUpdate',{'text':{'type':'string','minLength':1,'maxLength':8000},'financial_context':dict(context,required=[]),'expected_version':dict(integer,minimum=1)},['text','expected_version'],True)
model('GoalParseInput',{'expected_version':dict(integer,minimum=1)},strict=True)
model('GoalDifference',{'field':string,'before':{'nullable':True},'after':{'nullable':True}})
model('GoalComparison',{'from':integer,'to':integer,'differences':{'type':'array','items':ref('GoalDifference')}})
for n in ('LifeGoalView','StoredTaskPlan','GoalComparison'):
    schemas[n+'Response']={'allOf':[ref('Envelope'),{'type':'object','properties':{'data':ref(n)},'required':['data']}]}
schemas['LifeGoalVersionListResponse']={'allOf':[ref('Envelope'),{'type':'object','properties':{'data':{'type':'array','items':ref('LifeGoalVersion')}},'required':['data']}]}

def route(path,method,operation,result,body=None,create=False,params=None):
    p=[{'$ref':'#/components/parameters/TraceID'},{'$ref':'#/components/parameters/TaskID'}]
    if '{id}' in path: p.append({'name':'id','in':'path','required':True,'schema':ID})
    if create:p.append({'$ref':'#/components/parameters/IdempotencyKey'})
    p.extend(params or [])
    op={'operationId':operation,'tags':['GoalCompiler'],'security':[{'UserBearer':[]}],'parameters':p,'responses':{}}
    op['description']='Compiles plans only; never creates a transfer intent or modifies funds. Goal ownership comes from JWT. Unresolved plans block budget/savings/proposal tasks. Parsed versions and compiled plans are immutable after success.'
    if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
    op['responses']['201' if create else '200']={'description':'Persisted goal/plan result','content':{'application/json':{'schema':ref(result)}}}
    for code,desc in [('400','INVALID_ARGUMENT'),('401','UNAUTHENTICATED'),('404','NOT_FOUND'),('409','VERSION_CONFLICT / IDEMPOTENCY_CONFLICT / PARSE_IN_PROGRESS / PARSE_OUTCOME_UNKNOWN'),('422','MODEL_OUTPUT_INVALID / INVALID_TASK_PLAN / ESSENTIAL_RESERVE_CONFLICT'),('503','COZE_UNAVAILABLE / DEPENDENCY_UNAVAILABLE')]:
        op['responses'][code]={'description':desc,'content':{'application/json':{'schema':ref('Envelope')}}}
    doc['paths'].setdefault(path,{})[method]=op
version=[{'name':'version','in':'query','schema':dict(integer,minimum=1),'description':'Omit for current version'}]
pagination=[{'name':'limit','in':'query','schema':{'type':'integer','minimum':0,'maximum':1000000,'default':20},'description':'Capped at 100'},{'name':'offset','in':'query','schema':{'type':'integer','minimum':0,'maximum':1000000,'default':0}}]
route('/api/v1/goals','post','createLifeGoal','LifeGoalViewResponse','LifeGoalInput',True)
route('/api/v1/goals/{id}','get','getLifeGoal','LifeGoalViewResponse',params=version)
route('/api/v1/goals/{id}','put','updateLifeGoal','LifeGoalViewResponse','LifeGoalUpdate')
route('/api/v1/goals/{id}/parse','post','parseAndCompileLifeGoal','LifeGoalViewResponse','GoalParseInput')
route('/api/v1/goals/{id}/plan','get','getCompiledTaskPlan','StoredTaskPlanResponse',params=version)
route('/api/v1/goals/{id}/versions','get','listLifeGoalVersions','LifeGoalVersionListResponse',params=pagination)
route('/api/v1/goals/{id}/compare','get','compareLifeGoalVersions','GoalComparisonResponse',params=[{'name':n,'in':'query','required':True,'schema':dict(integer,minimum=1)} for n in ('from','to')])
doc['info']['title']='Qingyingjin Banking and Goal Compiler API';doc['info']['version']='0.3.0'
doc['info']['description']+=' Module3 Goal compiler uses a configured Coze workflow and JSON Schema validation; no financial execution. Monthly income is never inferred from account balance.'
doc['tags']=[t for t in doc.get('tags',[]) if t['name']!='GoalCompiler']+[{'name':'GoalCompiler'}]
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
print('Generated goal contract:',len(doc['paths']),'paths')
