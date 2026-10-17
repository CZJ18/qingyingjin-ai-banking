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
model('AAShareInput',{'name':{'type':'string','minLength':1,'maxLength':64},'amount_minor':num})
model('AABillInput',{'title':{'type':'string','minLength':1,'maxLength':128},'total_minor':dict(num,minimum=2),'mode':{'type':'string','enum':['equal','custom']},'account_id':uid,'already_paid':{'type':'boolean','enum':[True]},'owner_share_minor':num,'participants':{'type':'array','minItems':1,'maxItems':19,'items':ref('AAShareInput')}})
model('AAParticipant',{'id':uuid,'name':{'type':'string'},'amount_minor':num,'status':{'type':'string','enum':['self','unclaimed','joined','paid']},'is_me':{'type':'boolean'},'paid_at':{'type':'integer','format':'int64','minimum':0},'operation_id':uuid},['id','name','amount_minor','status','is_me','paid_at'])
model('AABillView',{'id':uuid,'owner_id':uid,'title':{'type':'string'},'total_minor':num,'currency':{'type':'string','enum':['CNY']},'mode':{'type':'string','enum':['equal','custom']},'status':{'type':'string','enum':['open','settled','cancelled']},'version':{'type':'integer','format':'int64','minimum':1},'is_owner':{'type':'boolean'},'invitation':{'type':'string','description':'Private invitation; owner only. Never log or put in a URL.'},'collected_minor':num,'outstanding_minor':num,'participants':{'type':'array','minItems':2,'maxItems':20,'items':ref('AAParticipant')},'created_at':{'type':'integer','format':'int64','minimum':0}},['id','owner_id','title','total_minor','currency','mode','status','version','is_owner','collected_minor','outstanding_minor','participants','created_at'])
model('AAInvitationInput',{'invitation':{'type':'string','minLength':100,'maxLength':128}})
model('AAJoinInput',{'invitation':{'type':'string','minLength':100,'maxLength':128},'participant_id':uuid})
model('AACancelInput',{'expected_version':{'type':'integer','format':'int64','minimum':1}})
model('AAPaymentInput',{'owner_id':uid,'bill_id':uuid,'participant_id':uuid,'source_account_id':uid})
for name,payload in [('AABillResponse',ref('AABillView')),('AABillsResponse',{'type':'array','maxItems':50,'items':ref('AABillView')})]:s[name]={'allOf':[ref('Envelope'),{'type':'object','required':['data'],'properties':{'data':payload}}]}
for method,path,name,body,response,success in [('get','/api/v1/aa/bills','listAABills',None,'AABillsResponse','200'),('post','/api/v1/aa/bills','createAABill','AABillInput','AABillResponse','201'),('get','/api/v1/aa/bills/{owner}/{id}','getAABill',None,'AABillResponse','200'),('post','/api/v1/aa/invitations/preview','previewAAInvitation','AAInvitationInput','AABillResponse','200'),('post','/api/v1/aa/join','joinAABill','AAJoinInput','AABillResponse','200'),('post','/api/v1/aa/bills/{id}/cancel','cancelAABill','AACancelInput','AABillResponse','200')]:
 op={'operationId':name,'tags':['AABills'],'security':[{'UserBearer':[]}],'description':'Human-only simulated prepaid AA bill. No debit without a separate aa_payment gateway intent, bound consent and execute. Invite claims membership only; cancellation stops unpaid shares without refunding paid shares.','responses':{success:{'description':'Success','content':{'application/json':{'schema':ref(response)}}}}}
 for code in ['400','401','403','404','409','422','503']:op['responses'][code]={'description':'Error','content':{'application/json':{'schema':ref('Envelope')}}}
 params=[]
 if '{owner}' in path:params.append({'name':'owner','in':'path','required':True,'schema':uid})
 if '{id}' in path:params.append({'name':'id','in':'path','required':True,'schema':uuid})
 if name=='createAABill':params.append({'name':'Idempotency-Key','in':'header','required':True,'schema':{'type':'string','minLength':1,'maxLength':128}})
 if name=='listAABills':params.extend([{'name':'limit','in':'query','schema':{'type':'integer','minimum':1,'maximum':50,'default':50}},{'name':'offset','in':'query','schema':{'type':'integer','minimum':0,'maximum':150,'default':0}}])
 if params:op['parameters']=params
 if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
 d['paths'].setdefault(path,{})[method]=op
for n in ['ExecutionPrepareInput','ExecutionOperation']:
 prop=s[n]['properties'].get('kind',{})
 if 'enum' in prop and 'aa_payment' not in prop['enum']:prop['enum'].append('aa_payment')
s['ExecutionPrepareInput']['properties']['parameters']['description']='Typed by kind. aa_payment uses AAPaymentInput; amount/recipient resolved from stored bill, human-only. Existing kinds unchanged.'
d['info']['version']='0.19.0'
p.write_text(yaml.safe_dump(d,allow_unicode=True,sort_keys=False,width=100),encoding='utf-8')
print('AA API 0.19.0 generated')
