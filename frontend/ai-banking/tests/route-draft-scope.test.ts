import {afterEach,expect,it,vi} from 'vitest'
import {createPinia,setActivePinia} from 'pinia'
import {useSession} from '../src/lib/store'
import {subscriptionManagementPath,subscriptionReference,subscriptionForecastView} from '../src/lib/subscription-scope'
afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals()})
function session(){setActivePinia(createPinia());const s=useSession();s.connected=true;s.user={id:'42',display_name:'synthetic',status:'active',created_at:0,updated_at:0};s.accountId='7';return s}
it('keeps a bounded user/account draft in memory without browser storage or execution metadata',()=>{
 const s=session();const setItem=vi.fn();vi.stubGlobal('localStorage',{setItem,getItem:()=>null})
 s.saveAssistantDraft({conversation:{input:'下月少花500元',mode:'general',analysisMonth:'2026-09'}})
 expect(s.readAssistantDraft()?.conversation?.input).toBe('下月少花500元')
 const copy=s.readAssistantDraft()!;copy.conversation!.input='changed';expect(s.readAssistantDraft()?.conversation?.input).toBe('下月少花500元')
 expect(setItem).not.toHaveBeenCalled();expect(JSON.stringify(s.readAssistantDraft())).not.toMatch(/grant|authorization|pendingSubmission|nonce/)
})
it('clears drafts across account, login epoch and timeout boundaries',()=>{
 vi.useFakeTimers();const s=session();const save=()=>s.saveAssistantDraft({conversation:{input:'private draft',mode:'general',analysisMonth:'2026-09'}})
 save();s.accountId='8';expect(s.readAssistantDraft()).toBeNull();s.accountId='7';expect(s.readAssistantDraft()).toBeNull()
 save();s.sessionEpoch++;expect(s.readAssistantDraft()).toBeNull()
 save();vi.advanceTimersByTime(30*60*1000+1);expect(s.readAssistantDraft()).toBeNull()
 save();s.connected=false;expect(s.readAssistantDraft()).toBeNull()
})
it('carries only a valid account/time reference to user-wide subscription management, never an authorization',()=>{
 const params={account_id:'27',from:'2026-08-31T16:00:00.000Z',to:'2026-09-30T16:00:00.000Z',timezone:'Asia/Shanghai',grant:'must-not-copy'}
 const path=subscriptionManagementPath(params);expect(path).toContain('reference_account_id=27');expect(path).not.toContain('grant')
 expect(subscriptionReference(Object.fromEntries(new URL('http://localhost'+path).searchParams))).toEqual({accountId:'27',from:params.from,to:params.to,timezone:'Asia/Shanghai'})
 for(const value of [{...params,account_id:'0'},{...params,to:params.from},{...params,from:'not-a-date'}])expect(subscriptionManagementPath(value)).toBe('/subscriptions')
})
it('uses conditional estimates only for known active/cancelled records, not actual charges or available money',()=>{
 const base={subscriptions:[],active_recurring_forecast:{kind:'conditional_if_all_active_cancelled',monthly_minor:0},cancelled_avoided_spend:{kind:'estimated_avoided_future_spend',monthly_minor:8990}}
 expect(subscriptionForecastView(base)).toEqual({activeMinor:null,cancelledMinor:null})
 expect(subscriptionForecastView({...base,subscriptions:[{subscription:{status:'cancelled'}}]})).toEqual({activeMinor:null,cancelledMinor:8990})
 expect(subscriptionForecastView({...base,subscriptions:[{subscription:{status:'active'}}],active_recurring_forecast:{kind:'conditional_if_all_active_cancelled',monthly_minor:9900}}).activeMinor).toBe(9900)
 expect(subscriptionForecastView({...base,subscriptions:[{subscription:{status:'active'}}],active_recurring_forecast:{kind:'unknown',monthly_minor:9900}}).activeMinor).toBeNull()
})
