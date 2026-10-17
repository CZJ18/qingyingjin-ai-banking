"""Add verification/recovery contracts. No code-reading or cloud SMS tool exists."""
from pathlib import Path
from copy import deepcopy
import yaml

root = Path(__file__).resolve().parents[1]
path = root / 'api/openapi.yaml'
doc = yaml.safe_load(path.read_text(encoding='utf-8'))
doc['info']['version'] = '0.16.0'
s = doc['components']['schemas']
ref = lambda n: {'$ref': '#/components/schemas/' + n}
obj = lambda properties, required=None: {'type':'object','additionalProperties':False,'properties':properties,'required':list(properties) if required is None else required}
string = {'type':'string'}
code = {'type':'string','pattern':'^[0-9]{6}$','writeOnly':True,'description':'Single-use purpose-bound OTP, expires in five minutes; never echoed in responses.'}
challenge = {'type':'string','pattern':'^[A-Za-z0-9_-]{43}$'}
phone = s['PhoneCredentials']['properties']['phone']
phone['description'] = 'Mainland phone login identifier; verification method is reported separately.'
password = s['PhoneCredentials']['properties']['password']
s['PhoneRegistration']['properties'].update({'challenge_id':challenge,'code':code})
s['PhoneRegistration']['description'] = 'When capabilities.registration_verification_required is true, challenge_id and code are mandatory. No legacy identity is claimed.'
s['BrowserSession']['properties'].update({'phone_verified':{'type':'boolean'},'phone_verification_method':{'type':'string','enum':['','development','tencent'],'description':'development is a local test flow, never proof of actual phone ownership. Tencent delivery is not implemented in this release.'}})
s['BrowserSession']['required'] += [p for p in ['phone_verified','phone_verification_method'] if p not in s['BrowserSession']['required']]
s['SMSChallenge'] = obj({'challenge_id':challenge,'expires_in':{'type':'integer','enum':[300]},'retry_after':{'type':'integer','enum':[60]}})
s['SMSPhoneInput'] = obj({'phone':phone})
s['PhoneVerificationInput'] = obj({'challenge_id':challenge,'code':code,'current_password':password})
s['PasswordResetInput'] = obj({'phone':phone,'challenge_id':challenge,'code':code,'new_password':password})
s['AuthCapabilities'] = obj({'sms_enabled':{'type':'boolean'},'sms_mode':{'type':'string','enum':['disabled','development']},'registration_verification_required':{'type':'boolean'},'code_expires_in':{'type':'integer','enum':[300]},'resend_after':{'type':'integer','enum':[60]}})
# Standalone envelope avoids inheriting the historical unconstrained but
# non-nullable Envelope.data schema through allOf.
s['AuthEmptyResponse'] = deepcopy(s['Envelope'])
s['AuthEmptyResponse']['properties']['data'] = {'type':'object','nullable':True,'enum':[None]}
s['AuthEmptyResponse']['required'] = list(dict.fromkeys(s['AuthEmptyResponse'].get('required',[]) + ['data']))
for n in ['SMSChallenge','AuthCapabilities']:
    s[n+'Response']={'allOf':[ref('Envelope'),{'type':'object','required':['data'],'properties':{'data':ref(n)}}]}
doc['paths']['/api/v1/auth/logout']['post']['responses']['200']['content']['application/json']['schema']=ref('AuthEmptyResponse')

def response(description, schema='Envelope'):
    return {'description':description,'content':{'application/json':{'schema':ref(schema)}}}
for suffix,operation_id,body,result,owner,description in [
    ('sms/registration','requestRegistrationOTP','SMSPhoneInput','SMSChallengeResponse',False,'Request a purpose-bound registration code; disabled provider fails closed. Local mode writes encrypted Redis inbox, never sends SMS.'),
    ('sms/password-reset','requestPasswordResetOTP','SMSPhoneInput','SMSChallengeResponse',False,'Same delivery and response for known, unknown and unverified numbers. Only a matching verified user/version/provider binding can reset a password.'),
    ('sms/phone-verification','requestPhoneVerificationOTP','EmptyAuthRequest','SMSChallengeResponse',True,'Request a code for the authenticated user’s existing phone. Agent and caller-supplied ownership are forbidden.'),
    ('phone/verify','verifyAuthenticatedPhone','PhoneVerificationInput','AuthEmptyResponse',True,'Verify an existing phone using current password and its single-use challenge. Does not change phone number or grant financial authorization.'),
    ('password/reset','resetPasswordWithOTP','PasswordResetInput','AuthEmptyResponse',False,'Consume reset code once; SQL transaction locks and rechecks phone, version, provider and ownership. Change password, revoke old access/refresh and pre-reset legacy user JWT; no automatic login. SQL failure keeps the OTP consumed.'),
]:
    responses={'200':response(description,result),'400':response('INVALID_ARGUMENT'),'401':response('AUTH_CODE_INVALID or UNAUTHENTICATED; fixed generic failure'),'403':response('FORBIDDEN: Origin or Agent permission'),'429':response('AUTH_RATE_LIMITED: phone/IP/global and attempt limits'),'503':response('SMS_UNAVAILABLE, DEPENDENCY_UNAVAILABLE or STORAGE_FROZEN'),'500':response('INTERNAL_ERROR')}
    if suffix=='password/reset':
        responses['200']['headers']={'Set-Cookie':{'description':'Expired HttpOnly refresh cookie; data is null and no JWT is issued.','schema':string}}
    doc['paths']['/api/v1/auth/'+suffix]={'post':{'operationId':operation_id,'tags':['Authentication'],'description':description+' JSON only; no-store. Codes are never included in public HTTP or logs.','security':[{'UserBearer':[]}] if owner else [],'requestBody':{'required':True,'content':{'application/json':{'schema':ref(body)}}},'responses':responses}}
doc['paths']['/api/v1/auth/capabilities']={'get':{'operationId':'getAuthCapabilities','tags':['Authentication'],'security':[],'description':'Public configuration state only, no credentials. Same-origin browser fetch omits cookies and Bearer.','responses':{'200':response('Authentication mode without secrets','AuthCapabilitiesResponse')}}}
path.write_text(yaml.safe_dump(doc,sort_keys=False,allow_unicode=True,width=100),encoding='utf-8')
