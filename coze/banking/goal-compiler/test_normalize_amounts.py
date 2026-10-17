import json
import unittest
from normalize_amounts import normalize, FIELDS

class SourceAmountTests(unittest.TestCase):
    def run_case(self, evidence, value=100000):
        parsed = {field: None for field in FIELDS}
        parsed.update(savings_monthly_minor=value, evidence={'savings_monthly_minor': evidence})
        return json.loads(normalize(json.dumps(parsed), json.dumps({'text': evidence})))

    def test_exact_yuan_and_decimal_cents(self):
        for source, cents in [('每月储蓄100元', 10000), ('每月储蓄1.50元', 150), ('每月储蓄0元', 0), ('每月储蓄29分', 29)]:
            with self.subTest(source=source):
                self.assertEqual(self.run_case(source)['savings_monthly_minor'], cents)

    def test_ambiguous_missing_and_fractional_cents_rejected(self):
        for source in ['每月储蓄100元或200元', '合理储蓄', '每月储蓄0.5分', '每月储蓄-100元']:
            with self.subTest(source=source), self.assertRaises(ValueError):
                self.run_case(source)

    def test_untrusted_evidence_and_invalid_model_type_rejected(self):
        with self.assertRaises(ValueError):
            self.run_case('每月储蓄100元', '10000')
        parsed = {field: None for field in FIELDS}
        parsed.update(savings_monthly_minor=100, evidence={'savings_monthly_minor': '每月储蓄100元'})
        with self.assertRaises(ValueError):
            normalize(json.dumps(parsed), json.dumps({'text': '保持规律运动'}))

if __name__ == '__main__':
    unittest.main()
