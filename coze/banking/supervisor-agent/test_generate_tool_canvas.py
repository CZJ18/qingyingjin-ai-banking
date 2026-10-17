import unittest
from generate_tool_canvas import generate, TOOLS

class SupervisorToolsTest(unittest.TestCase):
    def test_native_readonly_tools(self):
        for tool in TOOLS:
            canvas = generate(tool,'123','456','TEST ONLY NOT PUBLISHED')
            self.assertEqual([n['type'] for n in canvas['nodes']],['1','4','2'])
            node=canvas['nodes'][1]
            fields={p['name'] for p in node['data']['inputs']['inputParameters']}
            self.assertIn('job_id', fields)
            self.assertFalse(fields & {'user_id','decision','X-Banking-Tool-Key','Idempotency-Key'})
            self.assertEqual(next(p for p in node['data']['outputs'] if p['name']=='data')['type'],'object')
    def test_reject_sensitive_tool_or_fake_ids(self):
        for tool,pid in [('submit_orchestration','123'),('confirm_orchestration_task','123'),('get_orchestration','0001')]:
            with self.assertRaises(ValueError): generate(tool,pid,'456','TEST ONLY')

if __name__=='__main__': unittest.main()
