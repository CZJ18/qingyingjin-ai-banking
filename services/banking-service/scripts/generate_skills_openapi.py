"""Append immutable Skill catalog; preserve native plugin permissions."""
import copy
from pathlib import Path
import yaml

target=Path(__file__).resolve().parents[1]/'api/openapi.yaml'
doc=yaml.safe_load(target.read_text(encoding='utf-8'))
s=doc['components']['schemas']
string={'type':'string'}
def ref(name):return {'$ref':'#/components/schemas/'+name}
def obj(props):return {'type':'object','additionalProperties':False,'properties':props,'required':list(props)}
s['SkillPair']=obj({'agent_type':string,'tool':string,'execution_mode':string})
s['SkillDefinition']=obj({'skill_id':string,'skill_version':{'type':'integer','format':'int64'},'owner':string,'description':string,'allowed_pairs':{'type':'array','items':ref('SkillPair')},'internal_entry':string,'required_evidence':{'type':'array','items':string},'rule_version':string})
response=copy.deepcopy(s['Envelope']);response['properties']['data']={'type':'array','items':ref('SkillDefinition')};response['required']=list(dict.fromkeys(response.get('required',[])+['data']))
s['SkillCatalogResponse']=response
doc['paths']['/api/v1/skills']={'get':{'operationId':'get_skill_catalog','tags':['Skills'],'security':[{'UserBearer':[]}],'description':'Immutable capability catalog filtered by signed Agent role. Listing does not grant execution permission.','responses':{code:{'description':description,'content':{'application/json':{'schema':ref(schema)}}} for code,description,schema in [('200','Role-scoped catalog','SkillCatalogResponse'),('401','UNAUTHENTICATED','Envelope'),('403','FORBIDDEN','Envelope')]}}}
doc['info']['version']='0.9.0'
target.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False),encoding='utf-8')
