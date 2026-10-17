"""Generate the human-only AA contract additions; never adds Agent tools."""
from pathlib import Path
import yaml
p=Path(__file__).resolve().parents[1]/'api/openapi.yaml'
d=yaml.safe_load(p.read_text(encoding='utf-8')); s=d['components']['schemas']
ref=lambda n:{'$ref':'#/components/schemas/'+n}
num={'type':'integer','format':'int64','minimum':0,'maximum':1000000000000}
uid={'type':'string','pattern':'^[1-9][0-9]*$'}
uuid={'type':'string','format':'uuid'}
def model(n,props,required=None):s[n]={'type':'object','additionalProperties':False,'properties':props,'required':list(props) if required is None else required}
model('RecipientSettings',{'enabled':{'type':'boolean'},'account_id':{'type':'string','pattern':'^(0|[1-9][0-9]*)$'},'version':{'type':'integer','format':'int64','minimum':1}})
model('RecipientSettingsInput',{'enabled':{'type':'boolean'},'account_id':{'type':'string','pattern':'^(0|[1-9][0-9]*)$'},'expected_version':{'type':'integer','format':'int64','minimum':1}})
model('RecipientSearchInput',{'kind':{'type':'string','enum':['name','phone']},'query':{'type':'string','minLength':1,'maxLength':128,'description':'Exact registered display name (at least two characters), or full phone. Never put this field in a URL or log.'}})
model('RecipientView',{'id':uid,'display_name':{'type':'string'},'phone_masked':{'type':'string'},'account_name':{'type':'string'},'account_masked_number':{'type':'string'},'version':{'type':'integer','format':'int64','minimum':1},'phone_verification_method':{'type':'string','description':'development means local simulated verification, not phone-network proof or legal-name verification.'}})
model('RecipientSearchResult',{'items':{'type':'array','maxItems':20,'items':ref('RecipientView')},'limited':{'type':'boolean'}})
model('RecipientTransferInput',{'recipient_id':uid,'recipient_version':{'type':'integer','format':'int64','minimum':1},'source_account_id':uid,'amount_minor':dict(num,minimum=1),'description':{'type':'string','minLength':1,'maxLength':512}})
for name,payload in [('RecipientSettingsResponse',ref('RecipientSettings')),('RecipientSearchResponse',ref('RecipientSearchResult'))]:s[name]={'allOf':[ref('Envelope'),{'type':'object','required':['data'],'properties':{'data':payload}}]}
for method,path,name,body,response in [('get','/api/v1/recipients/settings','getRecipientSettings',None,'RecipientSettingsResponse'),('put','/api/v1/recipients/settings','updateRecipientSettings','RecipientSettingsInput','RecipientSettingsResponse'),('post','/api/v1/recipients/search','searchRecipients','RecipientSearchInput','RecipientSearchResponse')]:
 op={'operationId':name,'tags':['RecipientTransfers'],'security':[{'UserBearer':[]}],'description':'Human-only opt-in directory. Requires a verified registered phone and usable simulated CNY receiving account. Name is display name, not legally verified identity. Exact lookup; select among same-name results. Search is limited to 20/user and 60/IP per minute, Redis failure denies search. No money moves until separate recipient_transfer consent and execution.','responses':{'200':{'description':'Success','content':{'application/json':{'schema':ref(response)}}}}}
 for code in ['400','401','403','404','409','422','429','503']:op['responses'][code]={'description':'Error','content':{'application/json':{'schema':ref('Envelope')}}}
 if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
 d['paths'].setdefault(path,{})[method]=op
for n in ['ExecutionPrepareInput','ExecutionOperation']:
 prop=s[n]['properties'].get('kind',{})
 if 'enum' in prop and 'recipient_transfer' not in prop['enum']:prop['enum'].append('recipient_transfer')
s['ExecutionPrepareInput']['properties']['parameters']['description']='Typed by kind. recipient_transfer uses RecipientTransferInput (human-only, recipient account resolved by server); aa_payment uses AAPaymentInput. Other kinds unchanged.'
d['info']['version']='0.20.0'
p.write_text(yaml.safe_dump(d,allow_unicode=True,sort_keys=False,width=100),encoding='utf-8')
print('Recipient transfers API 0.20.0 generated')
