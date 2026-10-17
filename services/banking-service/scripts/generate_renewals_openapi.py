"""Add actual user-only station renewal-notification contracts."""
from pathlib import Path
import yaml
path=Path(__file__).resolve().parents[1]/'api/openapi.yaml'
doc=yaml.safe_load(path.read_text(encoding='utf-8'));s=doc['components']['schemas']
ref=lambda name:{'$ref':'#/components/schemas/'+name}
integer={'type':'integer','format':'int64','minimum':0}
def schema(name,props,required=None):s[name]={'type':'object','additionalProperties':False,'properties':props,'required':list(props) if required is None else required}
schema('RenewalSettings',{'enabled':{'type':'boolean'},'lead_days':{'type':'integer','minimum':0,'maximum':30},'version':dict(integer,minimum=1)})
schema('RenewalNotice',{'id':{'type':'string','format':'uuid'},'subscription_id':{'type':'string','pattern':'^[1-9][0-9]*$'},'merchant_name':{'type':'string'},'amount_minor':dict(integer,minimum=1,maximum=1000000000000),'currency':{'type':'string','enum':['CNY']},'charge_at':integer,'status':{'type':'string','enum':['unread','read','withdrawn','expired']},'reason':{'type':'string'},'version':dict(integer,minimum=1),'read_at':integer,'created_at':integer,'updated_at':integer})
schema('RenewalNoticeView',{'settings':ref('RenewalSettings'),'items':{'type':'array','maxItems':200,'items':ref('RenewalNotice')},'last_checked_at':integer,'capacity_limited':{'type':'boolean'},'unread_count':{'type':'integer','minimum':0,'maximum':200}})
schema('RenewalSettingsInput',{'enabled':{'type':'boolean'},'lead_days':{'type':'integer','minimum':0,'maximum':30},'expected_version':dict(integer,minimum=1)})
schema('RenewalReadInput',{'expected_version':dict(integer,minimum=1)})
s['RenewalNoticeResponse']={'allOf':[ref('Envelope'),{'type':'object','properties':{'data':ref('RenewalNoticeView')},'required':['data']}]}
for method,endpoint,name,body in [('get','/api/v1/notifications/renewals','listRenewalNotices',None),('put','/api/v1/notifications/renewals/settings','updateRenewalSettings','RenewalSettingsInput'),('post','/api/v1/notifications/renewals/{id}/read','readRenewalNotice','RenewalReadInput')]:
 op={'operationId':name,'tags':['RenewalReminders'],'security':[{'UserBearer':[]}],'description':'Human user only. Persistent in-app predicted renewal reminders; never renews, pays or sends SMS. The background worker scans every minute; GET never creates notices. Lead days use Asia/Shanghai calendar dates.','responses':{'200':{'description':'success','content':{'application/json':{'schema':ref('RenewalNoticeResponse')}}}}}
 for code in ['400','401','403','404','409','503']:op['responses'][code]={'description':'Error','content':{'application/json':{'schema':ref('Envelope')}}}
 if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
 if '{id}' in endpoint:op['parameters']=[{'name':'id','in':'path','required':True,'schema':{'type':'string','format':'uuid'}}]
 doc['paths'].setdefault(endpoint,{})[method]=op
doc['info']['version']='0.18.0'
path.write_text(yaml.safe_dump(doc,allow_unicode=True,sort_keys=False,width=100),encoding='utf-8')
print('Renewal notification API 0.18.0 generated')
