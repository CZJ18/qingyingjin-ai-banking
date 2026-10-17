import json
import unittest
from generate_goal_canvas import generate

class GoalCanvasTest(unittest.TestCase):
    def test_native_nodes(self):
        canvas = generate('123', 'TEST ONLY')
        self.assertEqual(sorted(n['type'] for n in canvas['nodes']), ['1', '2', '3'])
        end = next(n for n in canvas['nodes'] if n['type'] == '2')
        llm = next(n for n in canvas['nodes'] if n['type'] == '3')
        self.assertEqual(end['data']['inputs']['inputParameters'][0]['input']['value']['content']['blockID'], llm['id'])
        llm = next(n for n in canvas['nodes'] if n['type'] == '3')
        self.assertNotIn('fcParam', llm['data']['inputs'])
        self.assertEqual(llm['data']['inputs']['settingOnError']['retryTimes'], 0)
    def test_missing_period_not_defaulted(self):
        canvas = generate('123','TEST ONLY')
        params={f['name']:f['input']['value']['content'] for f in next(n for n in canvas['nodes'] if n['type']=='3')['data']['inputs']['llmParam']}
        self.assertIn('needs_information',params['systemPrompt'])
        self.assertIn('expense_reduction_monthly_minor',params['systemPrompt'])
    def test_invalid_model_id(self):
        with self.assertRaises(ValueError):generate('0001','TEST ONLY')

if __name__ == '__main__': unittest.main()
