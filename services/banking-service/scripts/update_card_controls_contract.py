"""Add backward compatible controls to the existing simulated card contract."""
from pathlib import Path
import yaml

p=Path(__file__).resolve().parents[1]/'api/openapi.yaml'
d=yaml.safe_load(p.read_text(encoding='utf8'));s=d['components']['schemas']
limit={'type':'integer','format':'int64','minimum':0,'maximum':1000000000000}
s['BankCard']['properties']['transaction_limit_minor']=dict(limit,description='Optional legacy field. Zero explicitly blocks card-initiated transfers; absence retains account risk policy. Not a cash balance.')
s['BankCard']['properties']['status']['enum']=['active','locked','lost','unbound']
inp=s['ExecutionCardManageParameters']
inp['properties']['action']['enum']=['bind','apply','unbind','set_default','adjust_limit','set_transaction_limit','lock','unlock','report_lost']
inp['properties']['transaction_limit_minor']=limit
inp['description']='User-only simulated metadata. apply/bind require account, issuer, name, four digits, card type. Other actions require card_id; adjust_limit requires an explicit positive credit_limit_minor, set_transaction_limit requires transaction_limit_minor including zero. Missing/zero credit adjustments are rejected. Lost cards cannot be unlocked; locked cards may be reported lost directly. All writes require prepare/authorize/execute; no PAN/CVV or bank integration.'
for name in ['TransferInput','ExecutionTransferParameters']:
 s[name]['properties']['card_id']={'type':'string','format':'uuid','description':'Optional debit card initiation, owned by the source account. Enforced at preparation and execution; locked/lost/unbound and over-limit cards are rejected. No credit spending.'}
p.write_text(yaml.safe_dump(d,allow_unicode=True,sort_keys=False,width=100),encoding='utf8')
print('Updated existing card and transfer schemas; retained version and existing required fields.')
