"""Exercise native schema mapping, preserved IDs and denied execution tools."""
import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location('savings_adapter', Path(__file__).with_name('generate_tool_canvas.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class SavingsCanvasTest(unittest.TestCase):
    def test_all_advisory_wrappers_preserve_native_data(self):
        for tool in module.TOOLS:
            with self.subTest(tool=tool):
                canvas = module.generate(tool, '123', '456', 'TEST_ONLY_Savings', 'TEST_ONLY_v1')
                start, plugin, end = canvas['nodes']
                self.assertEqual([n['type'] for n in canvas['nodes']], ['1', '4', '2'])
                self.assertEqual([f['name'] for f in start['data']['outputs']], ['plan_id', 'trace_id', 'task_id'])
                bindings = {p['name']: p['input']['value']['content'] for p in plugin['data']['inputs']['apiParam']}
                self.assertEqual(bindings['pluginID'], '123')
                self.assertEqual(bindings['apiID'], '456')
                self.assertEqual(bindings['apiName'], tool)
                inputs = plugin['data']['inputs']['inputParameters']
                self.assertEqual([p['name'] for p in inputs], ['plan_id', 'X-Trace-ID', 'X-Task-ID'])
                for p in inputs:
                    self.assertEqual(p['input']['value']['content']['blockID'], start['id'])
                self.assertEqual(plugin['data']['inputs']['settingOnError']['retryTimes'], 0)
                data = next(f for f in plugin['data']['outputs'] if f['name'] == 'data')
                fields = {f['name']: f for f in data['schema']}
                self.assertEqual(fields['result']['type'], 'object')
                result_fields = {f['name']: f for f in fields['result']['schema']}
                self.assertEqual(result_fields['options']['type'], 'list')
                self.assertEqual(result_fields['savings_cap_minor']['type'], 'integer')
                self.assertEqual(end['data']['inputs']['terminatePlan'], 'returnVariables')
                end_data = next(f for f in end['data']['inputs']['inputParameters'] if f['name'] == 'data')
                self.assertEqual(end_data['input']['schema'], data['schema'])
                self.assertEqual(canvas['edges'], [{'sourceNodeID': start['id'], 'targetNodeID': plugin['id']}, {'sourceNodeID': plugin['id'], 'targetNodeID': end['id']}])

    def test_sensitive_tools_and_noncanonical_ids_rejected(self):
        for tool in ['approve_transfer', 'confirm_financial_plan_advice', 'create_cancel_intent', 'update_financial_plan_settings']:
            with self.subTest(tool=tool), self.assertRaises(ValueError):
                module.generate(tool, '123', '456', 'TEST_ONLY', 'TEST_ONLY_v1')
        for invalid in ['0', '01', '-1', 'abc', str(2**63)]:
            with self.subTest(plugin_id=invalid), self.assertRaises(ValueError):
                module.generate(module.TOOLS[0], invalid, '456', 'TEST_ONLY', 'TEST_ONLY_v1')
            with self.subTest(api_id=invalid), self.assertRaises(ValueError):
                module.generate(module.TOOLS[0], '123', invalid, 'TEST_ONLY', 'TEST_ONLY_v1')
        with self.assertRaises(ValueError):
            module.generate(module.TOOLS[0], '123', '456', ' ', 'TEST_ONLY_v1')

if __name__ == '__main__':
    unittest.main()
