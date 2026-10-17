"""Reuse the module5 native Coze Plugin wrapper adapter for advisory tools."""
import argparse
import importlib.util
import json
from pathlib import Path

TOOLS = ['get_financial_plan', 'replan_financial_plan', 'compare_financial_plans']
adapter_path = Path(__file__).resolve().parents[1] / 'subscription-agent/generate_tool_canvas.py'
spec = importlib.util.spec_from_file_location('banking_native_plugin_adapter', adapter_path)
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)

def generate(tool, plugin_id, api_id, plugin_name, plugin_version=''):
    if tool not in TOOLS:
        raise ValueError('Only advisory Savings tools allowed; no confirmation or execution')
    return adapter.generate_operation('/tools/v1/savings/', TOOLS, tool, plugin_id, api_id, plugin_name, plugin_version)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['tool', 'plugin-id', 'api-id', 'plugin-name', 'plugin-version', 'out']:
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    Path(args.out).write_text(json.dumps(generate(args.tool, args.plugin_id, args.api_id, args.plugin_name, args.plugin_version), ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print('Native advisory wrapper reference generated; actual native configuration/publishing required.')
