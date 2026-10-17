"""Extend existing AA contract with human-only manual WeChat reconciliation."""
from pathlib import Path
import yaml
p=Path(__file__).resolve().parents[1]/'api/openapi.yaml'
d=yaml.safe_load(p.read_text(encoding='utf-8'));s=d['components']['schemas']
ref=lambda name:{'$ref':'#/components/schemas/'+name}
integer={'type':'integer','format':'int64','minimum':0,'maximum':1000000000000}
uid={'type':'string','pattern':'^[1-9][0-9]*$'}
uuid={'type':'string','format':'uuid'}
def model(name,props):s[name]={'type':'object','additionalProperties':False,'properties':props,'required':list(props)}
model('AALineItem',{'name':{'type':'string','minLength':1,'maxLength':128},'amount_minor':dict(integer,minimum=1)})
extra={'payment_method':{'type':'string','enum':['simulation','wechat_manual'],'description':'Immutable channel; omitted means simulation. WeChat manual records never write financial balances.'},'recipient_hint':{'type':'string','maxLength':128,'description':'Owner supplied hint, not verified WeChat identity.'},'memo':{'type':'string','maxLength':400},'items':{'type':'array','maxItems':30,'items':ref('AALineItem'),'description':'Required for wechat_manual; positive amounts must sum exactly to total_minor.'}}
for name in ['AABillInput','AABillView']:s[name]['properties'].update(extra)
for name in ['AABillInput','AABillView']:
 s[name]['properties']['management_mode']={'type':'string','enum':['single_manager','collaborative'],'description':'New wechat_manual defaults single_manager. Historical stored omission means collaborative. Single manager has no invitations or payer self-report. Immutable.'}

s['AABillInput']['required']=[v for v in s['AABillInput']['required'] if v!='account_id']
s['AABillInput']['properties']['account_id']={'type':'string','pattern':'^(0|[1-9][0-9]*)$','description':'Required positive simulated account for simulation; omit or use 0 for wechat_manual.'}
s['AABillView']['properties']['platform_verified']={'type':'boolean','enum':[False]}
s['AAParticipant']['properties']['status']['enum']=['self','unclaimed','joined','paid','payment_reported','receipt_confirmed','awaiting_receipt']
for name in ['reported_at','received_at']:s['AAParticipant']['properties'][name]={'type':'integer','format':'int64','minimum':0}
review={'participant_id':uuid,'purpose':{'type':'string','enum':['report_paid','confirm_received']},'expected_version':{'type':'integer','format':'int64','minimum':1}}
model('AAManualReviewInput',review)
binding={'intent_id':uuid,'digest':{'type':'string','pattern':'^[0-9a-f]{64}$'},'nonce':{'type':'string','minLength':64,'maxLength':64}}
model('AAManualConfirmInput',{**review,**binding})
model('AAManualReview',{'bill':ref('AABillView'),'participant_id':uuid,'purpose':review['purpose'],**binding,'expires_at':{'type':'integer','format':'int64','minimum':1}})
s['AAManualReviewResponse']={'allOf':[ref('Envelope'),{'type':'object','required':['data'],'properties':{'data':ref('AAManualReview')}}]}
for action,body,response in [('review','AAManualReviewInput','AAManualReviewResponse'),('confirm','AAManualConfirmInput','AABillResponse')]:
 path='/api/v1/aa/wechat/{owner}/{id}/'+action
 d['paths'][path]={'post':{'operationId':action+'AAManualWeChat','tags':['AABills'],'security':[{'UserBearer':[]}],
  'description':'Human-only metadata, no WeChat payment API or financial execution. Review binds exact bill details, payer, amount, recipient hint and purpose. Confirm consumes a 15-minute nonce plus current version. Only payer reports; only owner confirms receipt. All results remain platform_verified=false.',
  'parameters':[{'name':'owner','in':'path','required':True,'schema':uid},{'name':'id','in':'path','required':True,'schema':uuid}],
  'requestBody':{'required':True,'content':{'application/json':{'schema':ref(body)}}},
  'responses':{str(code):{'description':'Success' if code==200 else 'Error','content':{'application/json':{'schema':ref(response if code==200 else 'Envelope')}}} for code in [200,400,401,403,404,409,422,503]}}}
d['info']['version']='0.22.0'
p.write_text(yaml.safe_dump(d,allow_unicode=True,sort_keys=False,width=100),encoding='utf-8')
print('AA manual WeChat contract generated')
