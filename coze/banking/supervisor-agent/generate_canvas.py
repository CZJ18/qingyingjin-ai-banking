"""Generate current native Start/LLM/End reference, never publish or invent IDs."""
import argparse
import importlib.util
import json
from pathlib import Path

directory=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('verified_goal_canvas',directory.parent/'goal-compiler/generate_canvas.py')
adapter=importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)

def generate(model_id,model_name,role='supervisor'):
    if role not in ('supervisor','billing','subscription','savings','lifestyle'):
        raise ValueError('Known agent role required')
    canvas=adapter.generate(model_id,model_name)
    if role=='supervisor':
        schema=json.loads((directory/'task-plan.schema.json').read_text(encoding='utf-8'))
        prompt=(directory/'system-prompt.md').read_text(encoding='utf-8')
    elif role=='lifestyle':
        schema=json.loads((directory.parent/'lifestyle-agent/explanation.schema.json').read_text(encoding='utf-8'))
        prompt=(directory.parent/'lifestyle-agent/explanation-prompt.md').read_text(encoding='utf-8')
    else:
        schema=json.loads((directory/'explanation.schema.json').read_text(encoding='utf-8'))
        prompt=(directory/'explanation-prompt.md').read_text(encoding='utf-8')+'\n当前角色：'+role
    for node in canvas['nodes']:
        if node['type']=='3':
            node['data']['nodeMeta']['title']='轻盈金 '+role
            for field in node['data']['inputs']['llmParam']:
                if field['name']=='systemPrompt':
                    field['input']['value']['content']=prompt+'\nJSON Schema:\n'+json.dumps(schema,ensure_ascii=False)
    return canvas

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--model-id',required=True);parser.add_argument('--model-name',required=True)
    parser.add_argument('--role',default='supervisor',choices=['supervisor','billing','subscription','savings','lifestyle'])
    parser.add_argument('--out',type=Path,required=True);args=parser.parse_args()
    args.out.write_text(json.dumps(generate(args.model_id,args.model_name,args.role),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
