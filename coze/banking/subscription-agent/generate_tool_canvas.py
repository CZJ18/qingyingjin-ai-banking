"""Generate native Start -> Plugin -> End wrapper from checked-in Coze source.
Supply actual published Plugin/API IDs. No public import API or deployment is assumed.
"""
import argparse,copy,json
from pathlib import Path
import yaml
root=Path(__file__).resolve().parents[3]
source=root/'backend/domain/workflow/internal/canvas/examples/code_plugin.json'
TOOLS=['detect_subscriptions','get_subscription_details','analyze_subscription_cost','suggest_subscription_actions','create_cancel_intent','get_subscription_status']
def canonical(v):
 if not str(v).isdigit() or str(int(v))!=str(v) or not 0<int(v)<2**63:raise ValueError('Actual canonical positive installation ID required')
 return str(v)
def resolve(schema,doc):
 if '$ref' in schema:return resolve(doc['components']['schemas'][schema['$ref'].split('/')[-1]],doc)
 return schema
def variable(name,schema,doc,required=False):
 schema=resolve(schema,doc);kind=schema['type'];v={'name':name,'type':'list' if kind=='array' else kind,'required':required and not schema.get('nullable',False)}
 if kind=='object':v['schema']=[variable(n,s,doc,n in schema.get('required',[])) for n,s in schema.get('properties',{}).items()]
 if kind=='array':v['schema']=variable('',schema['items'],doc)
 return v
def reference(name,kind,block,field):
 return {'name':name,'input':{'type':kind,'value':{'type':'ref','content':{'source':'block-output','blockID':block,'name':field},'rawMeta':{'type':1}}}}
def generate(tool,plugin_id,api_id,plugin_name,plugin_version=''):
 return generate_operation('/tools/v1/subscriptions/',TOOLS,tool,plugin_id,api_id,plugin_name,plugin_version)

def generate_operation(prefix,allowed_tools,tool,plugin_id,api_id,plugin_name,plugin_version=''):
 if tool not in allowed_tools:raise ValueError('Unknown or sensitive execution tool prohibited')
 plugin_id=canonical(plugin_id);api_id=canonical(api_id)
 if not plugin_name.strip():raise ValueError('Actual plugin name required')
 canvas=json.loads(source.read_text(encoding='utf-8'));nodes=canvas['nodes']
 start=copy.deepcopy(next(n for n in nodes if n['type']=='1'))
 plugin=copy.deepcopy(next(n for n in nodes if n['type']=='4'))
 end=copy.deepcopy(next(n for n in nodes if n['type']=='2'))
 doc=yaml.safe_load((root/'services/banking-service/api/coze-plugin.openapi.yaml').read_text(encoding='utf-8'))
 op=doc['paths'][prefix+tool]['post'];body=resolve(op['requestBody']['content']['application/json']['schema'],doc)
 # Only concrete inputs are exposed. Optional account filtering can be configured
 # in the native UI; detection wrappers require an explicit period to avoid nils.
 names=list(body['properties']) if tool!='detect_subscriptions' else ['from','to']
 start_fields=[variable(n,body['properties'][n],doc,True) for n in names]
 mapping={n:n for n in names}
 for header,alias in [('X-Trace-ID','trace_id'),('X-Task-ID','task_id')]:
  start_fields.append({'name':alias,'type':'string','required':True});mapping[header]=alias
 if tool=='create_cancel_intent':start_fields.append({'name':'idempotency_key','type':'string','required':True});mapping['Idempotency-Key']='idempotency_key'
 start['data']['outputs']=start_fields;start['data']['trigger_parameters']=copy.deepcopy(start_fields)
 values={'pluginID':plugin_id,'apiID':api_id,'pluginName':plugin_name,'apiName':tool,'pluginVersion':plugin_version,'tips':'','outDocLink':''}
 for param in plugin['data']['inputs']['apiParam']:param['input']['value']['content']=values.get(param['name'],'')
 kinds={v['name']:v['type'] for v in start_fields}
 plugin['data']['inputs']['inputParameters']=[reference(n,kinds[alias],start['id'],alias) for n,alias in mapping.items()]
 plugin['data']['inputs']['settingOnError']={'processType':1,'timeoutMs':25000,'retryTimes':0}
 plugin['data']['nodeMeta']['title']=tool;plugin['data']['nodeMeta']['subtitle']=plugin_name+':'+tool;plugin['data']['nodeMeta']['description']='Authorized Banking API; proposals and advice only'
 status='200';response=resolve(op['responses'][status]['content']['application/json']['schema'],doc)
 outputs=[variable(n,s,doc,n in response.get('required',[])) for n,s in response['properties'].items()]
 plugin['data']['outputs']=outputs
 end['data']['inputs']['terminatePlan']='returnVariables'
 end['data']['inputs']['inputParameters']=[reference(v['name'],v['type'],plugin['id'],v['name']) for v in outputs]
 for field,v in zip(end['data']['inputs']['inputParameters'],outputs):
  if 'schema' in v:field['input']['schema']=copy.deepcopy(v['schema'])
 canvas['nodes']=[start,plugin,end];canvas['edges']=[{'sourceNodeID':start['id'],'targetNodeID':plugin['id']},{'sourceNodeID':plugin['id'],'targetNodeID':end['id']}]
 return canvas
if __name__=='__main__':
 p=argparse.ArgumentParser(description=__doc__)
 for name in ['tool','plugin-id','api-id','plugin-name','out']:p.add_argument('--'+name,required=True)
 p.add_argument('--plugin-version',required=True);a=p.parse_args()
 Path(a.out).write_text(json.dumps(generate(a.tool,a.plugin_id,a.api_id,a.plugin_name,a.plugin_version),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
 print('Native wrapper reference generated; requires native configuration, publishing and live-instance validation.')
