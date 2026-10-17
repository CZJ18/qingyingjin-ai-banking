import unittest
from generate_canvas import generate
class CanvasTest(unittest.TestCase):
    def test_router_reference(self):
        canvas=generate('123','TEST ONLY NOT INSTALLED');llm=next(n for n in canvas['nodes'] if n['type']=='3');self.assertNotIn('fcParam',llm['data']['inputs'])
        prompt=next(p['input']['value']['content'] for p in llm['data']['inputs']['llmParam'] if p['name']=='systemPrompt');self.assertIn('urn:qingyingjin:billing-query:1',prompt);self.assertNotIn('urn:qingyingjin:goal-parse:1',prompt)
if __name__=='__main__':unittest.main()
