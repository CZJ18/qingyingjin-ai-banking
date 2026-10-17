"""Reuse the checked-in native Coze Plugin adapter; no invented import API."""
import argparse
import importlib.util
import json
from pathlib import Path
TOOLS=['get_lifestyle_goal','get_lifestyle_plan','compile_lifestyle_plan']
path=Path(__file__).resolve().parents[1]/'subscription-agent/generate_tool_canvas.py'
spec=importlib.util.spec_from_file_location('native_plugin_adapter',path)
adapter=importlib.util.module_from_spec(spec);spec.loader.exec_module(adapter)
def generate(tool,plugin_id,api_id,plugin_name,plugin_version=''):
 result=adapter.generate_operation('/tools/v1/lifestyle/',TOOLS,tool,plugin_id,api_id,plugin_name,plugin_version)
 if tool=='compile_lifestyle_plan':
  start,plugin,_=result['nodes']
  field={'name':'idempotency_key','type':'string','required':True}
  start['data']['outputs'].append(field);start['data']['trigger_parameters'].append(dict(field))
  plugin['data']['inputs']['inputParameters'].append(adapter.reference('Idempotency-Key','string',start['id'],'idempotency_key'))
 return result
if __name__=='__main__':
 parser=argparse.ArgumentParser(description=__doc__)
 for name in ['tool','plugin-id','api-id','plugin-name','plugin-version','out']:parser.add_argument('--'+name,required=True)
 args=parser.parse_args()
 Path(args.out).write_text(json.dumps(generate(args.tool,args.plugin_id,args.api_id,args.plugin_name,args.plugin_version),ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
