"""Generate a native canvas reference from the checked-in Coze example.
This is NOT an invented public import API or a published workflow. Supply the
model ID/name from this installation, then configure/publish in Coze Studio.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parents[3]
source = root/'backend/domain/workflow/internal/canvas/examples/llm_node_with_skills/llm_node_with_workflow_tool.json'
directory = Path(__file__).resolve().parent

def generate(model_id,model_name):
    if not str(model_id).isdigit() or str(int(model_id)) != str(model_id) or not 0 < int(model_id) < 2**63:
        raise ValueError('Use a canonical model ID from this Coze installation')
    if not model_name.strip(): raise ValueError('Model name required')
    canvas = json.loads(source.read_text(encoding='utf-8'))
    prompt = (directory/'system-prompt.md').read_text(encoding='utf-8')
    schema = (root/'services/banking-service/internal/goal/schemas/goal-parse.schema.json').read_text(encoding='utf-8')
    for node in canvas['nodes']:
        if node['type'] != '3':continue
        inputs=node['data']['inputs'];inputs.pop('fcParam',None)
        inputs['settingOnError']={'processType':1,'timeoutMs':25000,'retryTimes':0}
        values={'modelType':str(model_id),'modleName':model_name,'temperature':'0','maxTokens':'4096','responseFormat':'2','enableChatHistory':False,'systemPrompt':prompt+'\nJSON Schema:\n'+schema}
        for field in inputs['llmParam']:
            if field['name'] in values:field['input']['value']['content']=values[field['name']]
        node['data']['nodeMeta']['title']='健康目标事实解析'
    return canvas

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--model-id',required=True)
    parser.add_argument('--model-name',required=True)
    parser.add_argument('--out',required=True,type=Path)
    args=parser.parse_args()
    canvas=generate(args.model_id,args.model_name)
    args.out.write_text(json.dumps(canvas,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    print('Generated canvas reference; publishing and real-model verification are still required.')
if __name__=='__main__':main()
