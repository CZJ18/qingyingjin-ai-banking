"""Generate router canvas using module3's verified native three-node adapter.
Requires actual configured model ID/name; this does not publish a workflow.
"""
import argparse,importlib.util,json
from pathlib import Path
directory=Path(__file__).resolve().parent
root=directory.parents[2]
spec=importlib.util.spec_from_file_location('goal_canvas',directory.parent/'goal-compiler/generate_canvas.py')
adapter=importlib.util.module_from_spec(spec);spec.loader.exec_module(adapter)
def generate(model_id,model_name):
    canvas=adapter.generate(model_id,model_name)
    prompt=(directory/'query-prompt.md').read_text(encoding='utf-8')+'\nJSON Schema:\n'+(root/'services/banking-service/internal/billing/schemas/billing-query.schema.json').read_text(encoding='utf-8')
    for node in canvas['nodes']:
        if node['type']=='3':
            node['data']['nodeMeta']['title']='Billing只读问题路由'
            for field in node['data']['inputs']['llmParam']:
                if field['name']=='systemPrompt':field['input']['value']['content']=prompt
    return canvas
if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--model-id',required=True);parser.add_argument('--model-name',required=True);parser.add_argument('--out',type=Path,required=True);args=parser.parse_args()
    args.out.write_text(json.dumps(generate(args.model_id,args.model_name),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
