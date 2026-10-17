"""Build v2 Start/LLM/End from a checked-in native example; no publishing API.
Exact source amount conversion belongs to Go, avoiding raw LLM text in Code nodes.
"""
import argparse
import importlib.util
import json
from pathlib import Path

directory = Path(__file__).resolve().parent
root = directory.parents[2]
spec = importlib.util.spec_from_file_location('native_goal', directory.parent/'goal-compiler/generate_canvas.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)

def generate(model_id, model_name):
    canvas = adapter.generate(model_id, model_name)
    schema = json.loads((root/'services/banking-service/internal/lifestyle/goal-v2.schema.json').read_text(encoding='utf-8'))
    output_schema = {'anyOf': [schema, {'type': 'object', 'additionalProperties': False,
        'required': ['status', 'missing_information'], 'properties': {
            'status': {'const': 'needs_information'}, 'missing_information': {'type': 'array',
                'minItems': 1, 'uniqueItems': True, 'items': {'enum': ['period_months', 'goal_details']}}}}]}
    def compact(value):
        if isinstance(value,dict):return {k:compact(v) for k,v in value.items() if k not in ('$schema','$id','format','maximum','minimum','maxItems','maxLength','maxProperties')}
        if isinstance(value,list):return [compact(v) for v in value]
        return value
    llm = next(n for n in canvas['nodes'] if n['type'] == '3')
    for field in llm['data']['inputs']['llmParam']:
        if field['name'] == 'systemPrompt':
            field['input']['value']['content'] = (directory/'goal-v2-prompt.md').read_text(encoding='utf-8')+'\nJSON Schema:\n'+json.dumps(compact(output_schema), ensure_ascii=False)
    return canvas

if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--model-id', required=True); p.add_argument('--model-name', required=True)
    p.add_argument('--out', required=True, type=Path); a = p.parse_args()
    a.out.write_text(json.dumps(generate(a.model_id, a.model_name), ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
