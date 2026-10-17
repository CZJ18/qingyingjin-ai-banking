import json
from pathlib import Path
import unittest
import jsonschema
from generate_canvas import generate

class SkillContractTest(unittest.TestCase):
    def test_schema_legacy_and_binding(self):
        schema=json.loads(Path(__file__).with_name('task-plan.schema.json').read_text(encoding='utf-8'))
        task=dict(key='billing',agent_type='billing',tool='get_transactions',parameters={},dependencies=[],priority=0,execution_mode='read_only')
        jsonschema.validate({'tasks':[task]},schema)
        task.update(skill_id='billing_analysis',skill_version=1)
        jsonschema.validate({'tasks':[task]},schema)
        for field,value in [('skill_version',99),('skill_id','subscription_analysis')]:
            invalid=dict(task);invalid[field]=value
            with self.assertRaises(jsonschema.ValidationError):jsonschema.validate({'tasks':[invalid]},schema)
        invalid=dict(task);del invalid['skill_version']
        with self.assertRaises(jsonschema.ValidationError):jsonschema.validate({'tasks':[invalid]},schema)
    def test_canvas_embeds_version_contract(self):
        canvas=generate('123','TEST ONLY','supervisor')
        llm=next(n for n in canvas['nodes'] if n['type']=='3')
        prompt=next(p['input']['value']['content'] for p in llm['data']['inputs']['llmParam'] if p['name']=='systemPrompt')
        self.assertIn('skill_version',prompt)
    def test_lifestyle_tool_mode_is_exact(self):
        schema=json.loads(Path(__file__).with_name('task-plan.schema.json').read_text(encoding='utf-8'))
        task=dict(key='lifestyle',agent_type='lifestyle',tool='get_lifestyle_plan',parameters={'goal_id':'0','plan_id':'123'},dependencies=[],priority=0,execution_mode='read_only',skill_id='lifestyle_planning',skill_version=1)
        jsonschema.validate({'tasks':[task]},schema)
        task['execution_mode']='advice'
        with self.assertRaises(jsonschema.ValidationError):jsonschema.validate({'tasks':[task]},schema)
