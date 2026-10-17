"""Append module6 advisory-only contracts with explicit native Coze schemas."""
import copy
from pathlib import Path
import yaml

root = Path(__file__).resolve().parents[1]
target = root / 'api/openapi.yaml'
doc = yaml.safe_load(target.read_text(encoding='utf-8'))
schemas = doc['components']['schemas']
ID = {'type': 'string', 'pattern': '^[1-9][0-9]*$'}
INT = {'type': 'integer', 'format': 'int64'}
MONEY = dict(INT, minimum=0)
STR = {'type': 'string'}
BOOL = {'type': 'boolean'}
def ref(name): return {'$ref': '#/components/schemas/' + name}
def array(item): return {'type': 'array', 'items': item}
def obj(properties, required=None):
    return {'type': 'object', 'additionalProperties': False, 'properties': properties,
            'required': list(properties) if required is None else required}
base = {'id': ID, 'user_id': ID, 'created_at': INT, 'updated_at': INT}
def record(name, props, optional=()):
    fields = dict(base, **props)
    schemas[name] = obj(fields, [k for k in fields if k not in optional])

schemas['PlanningCashflow'] = obj({'name': STR, 'kind': {'type': 'string', 'enum': ['income', 'necessary_expense']}, 'amount_minor': MONEY, 'due_at': INT})
ids = array(dict(INT, minimum=1))
settings = {'period': {'type': 'string', 'pattern': '^[0-9]{4}-(0[1-9]|1[0-2])$'}, 'account_ids': ids, 'savings_goal_ids': ids, 'life_goal_ids': ids,
            'living_remaining_minor': MONEY, 'essential_remaining_minor': MONEY,
            'safety_reserve_minor': MONEY, 'cash_buffer_minor': MONEY,
            'baseline_income_minor': MONEY, 'future_income_minor': MONEY,
            'cashflows': array(ref('PlanningCashflow'))}
schemas['PlanningSettings'] = obj(settings, [k for k in settings if k not in ('period', 'living_remaining_minor', 'essential_remaining_minor', 'future_income_minor')])
schemas['PlanningCreateInput'] = obj({'name': STR, 'settings': ref('PlanningSettings')})
schemas['PlanningUpdateInput'] = obj({'expected_version': dict(INT, minimum=1), 'settings': ref('PlanningSettings')})
schemas['PlanningIDInput'] = obj({'plan_id': ID})
schemas['PlanningDecisionInput'] = obj({'expected_version': dict(INT, minimum=1), 'data_digest': {'type': 'string', 'pattern': '^[a-f0-9]{64}$'}, 'option': {'type': 'string', 'enum': ['conservative', 'balanced', 'aggressive']}, 'confirm_advice': BOOL})
schemas['PlanningEventInput'] = obj({'type': {'type': 'string', 'enum': ['transaction_added', 'account_changed', 'necessary_expense_changed', 'subscription_changed', 'subscription_price_observed', 'life_goal_changed', 'savings_goal_changed', 'savings_executed']}, 'resource_id': dict(STR, minLength=1, maxLength=128), 'reason': dict(STR, minLength=1, maxLength=255)})
schemas['PlanningGoalUpdateInput'] = obj({'expected_target_minor': MONEY, 'expected_monthly_minor': MONEY, 'expected_ends_at': INT, 'target_minor': MONEY, 'monthly_minor': MONEY, 'ends_at': INT})
schemas['PlanningGoalFunds'] = obj({'id': ID, 'target_minor': MONEY, 'current_minor': MONEY, 'monthly_minor': MONEY, 'deposited_this_period_minor': INT, 'eligible': BOOL})
schemas['PlanningSource'] = obj({'type': STR, 'id': STR, 'version': INT})
snapshot = {'period': STR, 'settings': ref('PlanningSettings'), 'balance_minor': MONEY,
            'life_floor_minor': MONEY, 'essential_floor_minor': MONEY, 'subscription_due_minor': MONEY,
            'cancelled_forecast_minor': MONEY, 'goals': array(ref('PlanningGoalFunds')),
            'sources': array(ref('PlanningSource')), 'blockers': array(STR), 'review_notes': array(STR)}
schemas['PlanningSnapshot'] = obj(snapshot)
schemas['PlanningAllocation'] = obj({'goal_id': ID, 'amount_minor': MONEY, 'unmet_minor': MONEY, 'progress_bps': INT})
schemas['PlanningOption'] = obj({'name': STR, 'savings_minor': MONEY, 'retained_minor': MONEY, 'allocations': array(ref('PlanningAllocation')), 'explanation': STR, 'feasible': BOOL})
money_fields = ['balance_minor', 'necessary_reserve_minor', 'living_reserve_minor', 'additional_obligations_minor', 'cashflow_buffer_minor', 'safety_reserve_minor', 'income_risk_reserve_minor', 'disposable_minor', 'savings_cap_minor', 'suggested_savings_minor', 'target_demand_minor', 'future_income_minor', 'future_avoided_spend_minor', 'conflict_minor', 'reserve_shortfall_minor']
result = {k: MONEY for k in money_fields}
result.update({'currency': {'type': 'string', 'enum': ['CNY']}, 'period': STR, 'options': array(ref('PlanningOption')), 'blockers': array(STR), 'review_notes': array(STR), 'financial_execution_enabled': {'type': 'boolean', 'enum': [False]}})
schemas['PlanningResult'] = obj(result)
record('FinancialPlan', {'name': STR, 'current_version': INT, 'settings': ref('PlanningSettings'), 'status': STR})
record('FinancialPlanVersion', {'plan_id': ID, 'version': INT, 'previous_version': INT, 'event_id': ID, 'reason': STR, 'impact_minor': INT, 'data_digest': STR, 'needs_confirmation': BOOL, 'snapshot': ref('PlanningSnapshot'), 'result': ref('PlanningResult'), 'trace_id': STR, 'task_id': STR, 'audit_id': STR}, ('event_id',))
record('FinancialPlanConstraint', {'plan_version_id': ID, 'kind': STR, 'description': STR, 'amount_minor': MONEY})
record('FinancialPlanDecision', {'plan_id': ID, 'plan_version_id': ID, 'option': STR, 'data_digest': STR, 'audit_id': STR})
record('FinancialReplanEvent', {'event_key': STR, 'type': STR, 'resource_id': STR, 'reason': STR, 'status': STR, 'attempts': INT, 'retry_at': INT, 'error_code': STR, 'trace_id': STR, 'task_id': STR})
schemas['FinancialPlanView'] = obj({'plan': ref('FinancialPlan'), 'version': ref('FinancialPlanVersion'), 'result': ref('PlanningResult'), 'constraints': array(ref('FinancialPlanConstraint')), 'decisions': array(ref('FinancialPlanDecision')), 'financial_execution_enabled': {'type': 'boolean', 'enum': [False]}})
schemas['FinancialPlanVersionList'] = array(ref('FinancialPlanVersion'))
for name in ['FinancialPlanView', 'FinancialPlanVersionList', 'FinancialPlanDecision', 'FinancialReplanEvent']:
    envelope = copy.deepcopy(schemas['Envelope'])
    envelope['properties']['data'] = ref(name)
    envelope['required'] = list(dict.fromkeys(envelope.get('required', []) + ['data']))
    schemas[name + 'Response'] = envelope

def route(path, method, operation_id, body=None, result='FinancialPlanView', status='200', idem=False):
    tool = path.startswith('/tools/')
    params = [{'$ref': '#/components/parameters/TraceID'}, {'$ref': '#/components/parameters/TaskID'}]
    if '{id}' in path: params.append({'name': 'id', 'in': 'path', 'required': True, 'schema': ID})
    if idem: params.append({'$ref': '#/components/parameters/IdempotencyKey'})
    op = {'operationId': operation_id, 'tags': ['AdaptiveSavings'], 'security': [{'BankingToolKey' if tool else 'UserBearer': []}], 'parameters': params,
          'description': 'Advice only; all amounts are deterministic integer CNY cents. Never deposits forecast income/avoided subscription costs or executes transfers. UTC calendar-month period. Necessary cashflows are ADDITIONAL obligations outside living budget; do not enter the same expense twice. Tools cannot change settings or select advice. Snapshots and versions preserve evidence; decisions require exact version and data digest.', 'responses': {}}
    if body: op['requestBody'] = {'required': True, 'content': {'application/json': {'schema': ref(body)}}}
    for code, desc, name in [(status, 'Successful advisory result', result + 'Response'), ('400', 'INVALID_ARGUMENT', 'Envelope'), ('401', 'UNAUTHENTICATED', 'Envelope'), ('403', 'FORBIDDEN', 'Envelope'), ('404', 'NOT_FOUND including unowned resource', 'Envelope'), ('409', 'VERSION_CONFLICT / STATE_CONFLICT / IDEMPOTENCY_CONFLICT', 'Envelope'), ('422', 'RISK_REJECTED / ANALYSIS_LIMIT', 'Envelope'), ('503', 'DEPENDENCY_UNAVAILABLE', 'Envelope')]:
        op['responses'][code] = {'description': desc, 'content': {'application/json': {'schema': ref(name)}}}
    doc['paths'].setdefault(path, {})[method] = op

route('/api/v1/financial-plans', 'post', 'create_financial_plan', 'PlanningCreateInput', status='201')
route('/api/v1/financial-plans/{id}', 'get', 'get_user_financial_plan')
route('/api/v1/financial-plans/{id}/versions', 'get', 'get_financial_plan_versions', result='FinancialPlanVersionList')
doc['paths']['/api/v1/financial-plans/{id}/versions']['get']['parameters'].extend([
    {'name': 'limit', 'in': 'query', 'required': False, 'schema': dict(INT, minimum=1, maximum=100, default=100)},
    {'name': 'offset', 'in': 'query', 'required': False, 'schema': dict(INT, minimum=0, maximum=1000000, default=0)},
])
route('/api/v1/financial-plans/{id}/settings', 'put', 'update_financial_plan_settings', 'PlanningUpdateInput')
route('/api/v1/financial-plans/{id}/decisions', 'post', 'confirm_financial_plan_advice', 'PlanningDecisionInput', result='FinancialPlanDecision')
route('/api/v1/replan-events', 'post', 'publish_replan_event', 'PlanningEventInput', result='FinancialReplanEvent', status='201', idem=True)
route('/api/v1/savings/goals/{id}/settings', 'put', 'update_savings_goal_settings', 'PlanningGoalUpdateInput', result='SavingsGoal')
for name in ['get_financial_plan', 'replan_financial_plan', 'compare_financial_plans']:
    route('/tools/v1/savings/' + name, 'post', name, 'PlanningIDInput')
doc['info']['version'] = '0.6.0'
doc['info']['title'] = 'Qingyingjin Banking and Adaptive Savings API'
doc['tags'] = [t for t in doc.get('tags', []) if t['name'] != 'AdaptiveSavings'] + [{'name': 'AdaptiveSavings'}]
target.write_text(yaml.safe_dump(doc, allow_unicode=True, sort_keys=False), encoding='utf-8')
pt = root / 'api/coze-plugin.openapi.yaml'
plugin = yaml.safe_load(pt.read_text(encoding='utf-8'))
plugin['info']['version'] = '0.6.0'
plugin['info']['title'] = 'Qingyingjin Banking Agent Tools'
plugin['info']['description'] = 'Operator-bound user. Financial evidence, advisory planning and gateway-checked cancellation proposals only. No transfer, financial approval, settings writes or plan selection.'
for path, item in doc['paths'].items():
    if path.startswith('/tools/v1/savings/'): plugin['paths'][path] = item
for name, schema in schemas.items():
    if name.startswith(('Planning', 'Financial')): plugin['components']['schemas'][name] = schema
pt.write_text(yaml.safe_dump(plugin, allow_unicode=True, sort_keys=False), encoding='utf-8')
print('Adaptive Savings contracts generated:', len(doc['paths']), 'API paths;', len(plugin['paths']), 'plugin paths')
