"""Native Coze code node: normalize explicit source amounts, never account funds.

Model facts/evidence stay intact. Ambiguous or missing evidence fails closed;
the Banking parser independently checks units, frequency, meaning and schema.
"""
import json
import re
from decimal import Decimal

FIELDS = ('savings_monthly_minor', 'monthly_income_minor', 'spending_budget_minor',
          'essential_expenses_minor', 'savings_target_minor')

def normalize(model_output, original_input):
    parsed = json.loads(model_output)
    text = json.loads(original_input)['text']
    for field in FIELDS:
        value = parsed[field]
        if value is None:
            continue
        if type(value) is not int:
            raise ValueError('Model amount must be an integer or null')
        evidence = parsed['evidence'].get(field)
        if not isinstance(evidence, str) or not evidence or evidence not in text:
            raise ValueError('Amount evidence must be an original source substring')
        amounts = re.findall(r'(?<![\d.\-])([0-9]+(?:\.[0-9]{1,2})?)\s*(元|分)', evidence)
        if len(amounts) != 1:
            raise ValueError('Ambiguous amount evidence')
        number, unit = amounts[0]
        minor = Decimal(number) * (100 if unit == '元' else 1)
        if minor != minor.to_integral_value() or not 0 <= minor <= 1000000000000:
            raise ValueError('Source amount is outside the supported integer-cent range')
        parsed[field] = int(minor)
    return json.dumps(parsed, ensure_ascii=False, separators=(',', ':'))

async def main(args):
    return {'output': normalize(args.params['model_output'], args.params['original_input'])}
