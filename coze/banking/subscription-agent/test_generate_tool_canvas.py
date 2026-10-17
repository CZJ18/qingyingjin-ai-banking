import unittest
from generate_tool_canvas import generate,TOOLS
class CanvasTest(unittest.TestCase):
 def test_all_wrappers(self):
  for tool in TOOLS:
   c=generate(tool,'123','456','TEST ONLY NOT INSTALLED');self.assertEqual([n['type'] for n in c['nodes']],['1','4','2']);self.assertEqual(len(c['edges']),2)
   node=c['nodes'][1];params={p['name']:p['input']['value']['content'] for p in node['data']['inputs']['apiParam']}
   self.assertEqual(params['apiName'],tool);self.assertEqual(params['pluginID'],'123');self.assertEqual(params['apiID'],'456');self.assertEqual(node['data']['inputs']['settingOnError']['retryTimes'],0)
   fields={p['name'] for p in node['data']['inputs']['inputParameters']};self.assertNotIn('user_id',fields);self.assertNotIn('decision',fields);self.assertNotIn('X-Banking-Tool-Key',fields)
   if tool=='create_cancel_intent':self.assertIn('Idempotency-Key',fields)
   self.assertEqual(next(v for v in node['data']['outputs'] if v['name']=='data')['type'],'object')
 def test_invalid_and_execution_ids(self):
  for tool,pid,aid in [('confirm_local_subscription_cancel','123','456'),('detect_subscriptions','0001','2'),('detect_subscriptions','1','999999999999999999999')]:
   with self.assertRaises(ValueError):generate(tool,pid,aid,'TEST ONLY')
if __name__=='__main__':unittest.main()
