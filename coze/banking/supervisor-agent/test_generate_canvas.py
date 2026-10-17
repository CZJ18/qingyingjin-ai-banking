import unittest
from generate_canvas import generate

class SupervisorCanvasTest(unittest.TestCase):
    def test_roles_reuse_native_engine(self):
        for role in ['supervisor','billing','subscription','savings','lifestyle']:
            c=generate('123','TEST ONLY NOT CONFIGURED',role)
            self.assertEqual(sorted(n['type'] for n in c['nodes']),['1','2','3'])
            llm=next(n for n in c['nodes'] if n['type']=='3')['data']['inputs']
            self.assertNotIn('fcParam',llm)
            self.assertEqual(llm['settingOnError']['retryTimes'],0)
            params={p['name']:p['input']['value']['content'] for p in llm['llmParam']}
            self.assertEqual(params['temperature'],'0')
            self.assertIn('JSON Schema',params['systemPrompt'])
            self.assertIn('"tasks"' if role=='supervisor' else '"finding_ids"' if role=='lifestyle' else '"evidence_ids"',params['systemPrompt'])
    def test_invalid_configuration_rejected(self):
        for mid,role in [('0001','supervisor'),('123','unknown')]:
            with self.assertRaises(ValueError):generate(mid,'TEST ONLY',role)

if __name__=='__main__':unittest.main()
