import unittest
from generate_canvas import generate

class CanvasContractTest(unittest.TestCase):
    def test_native_reference(self):
        # Synthetic ID only tests canvas generation, not an installed model.
        canvas=generate('123','TEST MODEL NOT FOR DEPLOYMENT')
        nodes={n['id']:n for n in canvas['nodes']}
        self.assertEqual({'1','2','3'},{n['type'] for n in nodes.values()})
        inputs=nodes['159950']['data']['inputs']
        self.assertNotIn('fcParam',inputs)
        params={p['name']:p['input']['value']['content'] for p in inputs['llmParam']}
        self.assertEqual('123',params['modelType'])
        self.assertFalse(params['enableChatHistory'])
        self.assertIn('urn:qingyingjin:goal-parse:1',params['systemPrompt'])
        self.assertEqual('returnVariables',nodes['900001']['data']['inputs']['terminatePlan'])
        self.assertEqual(2,len(canvas['edges']))
    def test_invalid_model(self):
        for value in ['','0','001','invented-model','9223372036854775808']:
            with self.assertRaises(ValueError):generate(value,'name')

if __name__=='__main__':unittest.main()
