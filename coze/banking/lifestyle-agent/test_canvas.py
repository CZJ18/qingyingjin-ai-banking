import unittest
import generate_tool_canvas as canvas
class TestLifestyleCanvas(unittest.TestCase):
 def test_native_read_adapters(self):
  for tool in canvas.TOOLS:
   result=canvas.generate(tool,'1','2','fixture-only','test')
   self.assertEqual([n['type'] for n in result['nodes']],['1','4','2'])
   self.assertEqual(result['nodes'][1]['data']['inputs']['settingOnError']['retryTimes'],0)
 def test_execution_forbidden(self):
  with self.assertRaises(ValueError):canvas.generate('execute_transfer','1','2','fixture-only')
 def test_invalid_installation_ids(self):
  with self.assertRaises(ValueError):canvas.generate('get_lifestyle_goal','fake','2','fixture-only')
if __name__=='__main__':unittest.main()
