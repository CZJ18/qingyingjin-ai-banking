"""Record P3 executed evidence, without dumping credentials or inventing a Git diff."""
from collections import Counter
from pathlib import Path
import hashlib
import json
import re
import subprocess
import urllib.request

root = Path(__file__).resolve().parents[3]
out = root / 'services/banking-service/output/module11'


def load(name):
    return json.loads((out / name).read_text(encoding='utf-8-sig'))


def events(name):
    rows = [json.loads(x) for x in (out / name).read_text(encoding='utf-8-sig').splitlines() if x.strip()]
    tests = Counter(r['Action'] for r in rows if r.get('Test') and r['Action'] in ('pass', 'fail', 'skip'))
    packages = Counter(r['Action'] for r in rows if not r.get('Test') and r['Action'] in ('pass', 'fail', 'skip'))
    assert tests['pass'] and not tests['fail'] and packages['pass'] and not packages['fail']
    return {'test_events': {k: tests[k] for k in ('pass', 'fail', 'skip')},
            'packages': dict(packages),
            'skipped_tests': [r['Test'] for r in rows if r.get('Test') and r['Action'] == 'skip']}


def python_result(name):
    log = (out / name).read_text(encoding='utf-8-sig')
    match = re.search(r'Ran (\d+) tests? in', log)
    assert match and log.rstrip().endswith('OK')
    return {'passed': int(match[1]), 'log': name}


build = load('build-vet-final.json')
assert all(build[k] == 0 for k in ('unit_exit', 'build_exit', 'vet_exit'))
live, restart = load('live.json'), load('live-restart.json')
assert live['passed'] and live['real_model_executed'] and restart['passed'] and restart['restart']
state = load('live-state.json')
for result in (live, restart):
    outputs = result['responses']['result']['body']['data']['outputs']
    assert next(x for x in outputs if x['agent_type'] == 'lifestyle')['workflow_execute_id'] == next(
        x for x in live['responses']['result']['body']['data']['outputs'] if x['agent_type'] == 'lifestyle')['workflow_execute_id']
race = (out / 'race-final.log').read_text(encoding='utf-8-sig').strip()
assert len(re.findall(r'^ok\s+', race, re.MULTILINE)) == 3 and 'FAIL' not in race and 'DATA RACE' not in race
container = json.loads(subprocess.check_output(['docker', 'inspect', 'qingyingjin-banking-service-1'], encoding='utf-8'))[0]
assert container['Config']['Image'] == 'qingyingjin-banking-service:module11'
assert container['State']['Running'] and container['State']['Health']['Status'] == 'healthy'
with urllib.request.urlopen('http://127.0.0.1:8090/readyz', timeout=5) as response:
    readiness = json.load(response)
assert readiness['data']['dependencies'] == {'mysql': 'ready', 'redis': 'ready', 'schema': 'ready'}
# Expand passwords only inside the existing container; neither credentials nor environment are printed.
query = 'MYSQL_PWD="$MYSQL_PASSWORD" mysql -u "$MYSQL_USER" "$MYSQL_DATABASE" -Nse "SELECT MAX(version) FROM banking_schema_migrations"'
version = int(subprocess.check_output(['docker', 'exec', 'qingyingjin-banking-mysql-1', 'sh', '-c', query], encoding='utf-8').strip())
assert version == 10
(out / 'migration-version.txt').write_text(str(version) + '\n', encoding='utf-8')
private = json.loads((root / 'output/coze-local-private.json').read_text(encoding='utf-8'))
workflows = {role: {'id': w['id'], 'published': w['published'], 'revision': w.get('revision'),
                    'canvas_sha256': w.get('canvasHash')} for role, w in private['workflows'].items()}
assert all(workflows[r]['published'] for r in ('supervisor', 'billing', 'subscription', 'savings', 'lifestyle', 'goal_v2'))

paths = [
    'services/banking-service/MODULE11.md',
    'docs/superpowers/plans/2026-10-13-qingrongjin-model-p3.md',
    'docs/superpowers/plans/2026-10-13-qingrongjin-upgrade-roadmap.md',
    'services/banking-service/internal/lifestyle/parser.go',
    'services/banking-service/internal/lifestyle/parser_test.go',
    'services/banking-service/internal/lifestyle/amounts.go',
    'services/banking-service/internal/lifestyle/amounts_test.go',
    'services/banking-service/internal/lifestyle/schema.go',
    'services/banking-service/internal/lifestyle/schema_test.go',
    'services/banking-service/internal/orchestrator/model.go',
    'services/banking-service/internal/orchestrator/executor.go',
    'services/banking-service/internal/orchestrator/explanation.go',
    'services/banking-service/internal/orchestrator/explanation_test.go',
    'services/banking-service/internal/orchestrator/lifestyle_explanation.go',
    'services/banking-service/internal/orchestrator/lifestyle_explanation_test.go',
    'services/banking-service/scripts/generate_lifestyle_openapi.py',
    'services/banking-service/scripts/verify_model_live.py',
    'services/banking-service/scripts/record_model_delivery.py',
    'services/banking-service/api/openapi.yaml',
    'services/banking-service/api/coze-plugin.openapi.yaml',
    'coze/banking/lifestyle-agent/generate_goal_canvas.py',
    'coze/banking/lifestyle-agent/test_goal_canvas.py',
    'coze/banking/lifestyle-agent/goal-v2-prompt.md',
    'coze/banking/lifestyle-agent/goal-v2-workflow-config.yaml',
    'coze/banking/lifestyle-agent/agent-config.yaml',
    'coze/banking/lifestyle-agent/explanation-prompt.md',
    'coze/banking/lifestyle-agent/explanation.schema.json',
    'coze/banking/supervisor-agent/generate_canvas.py',
    'coze/banking/supervisor-agent/test_generate_canvas.py',
    'coze/banking/supervisor-agent/test_skill_contract.py',
    'coze/banking/supervisor-agent/system-prompt.md',
    'coze/banking/supervisor-agent/explanation-prompt.md',
    'coze/banking/supervisor-agent/task-plan.schema.json',
    'coze/banking/supervisor-agent/agent-config.yaml',
    'coze/banking/supervisor-agent/README.md',
    'deploy/banking/bootstrap-coze-local.mjs',
    'deploy/banking/compose.yaml',
    'deploy/banking/Build-Image.ps1',
]
files = [{'path': str(root / path), 'sha256': hashlib.sha256((root / path).read_bytes()).hexdigest()} for path in paths]
(out / 'files.json').write_text(json.dumps({'description': 'P3 intentionally implemented/updated files, current hashes; not a Git diff', 'files': files}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
report = {
    'build_vet': build, 'unit': events('unit-final.jsonl'), 'integration': events('integration-final.jsonl'),
    'race': race, 'python': {'supervisor': python_result('supervisor-python.log'), 'lifestyle': python_result('lifestyle-python.log')},
    'live': {'passed': live['passed'], 'real_model_executed': live['real_model_executed'], 'checks': live['checks'],
             'parse_execute_id': state['parse_execute_id'], 'job_id': state['job_id'],
             'executions': [{'agent_type': e['agent_type'], 'execute_id': e['workflow_execute_id']} for e in live['responses']['trace']['body']['data']['executions']]},
    'restart': {'passed': restart['passed'], 'checks': restart['checks']},
    'container': {'image': container['Config']['Image'], 'image_id': container['Image'], 'health': container['State']['Health']['Status']},
    'readiness': readiness['data'], 'migration_version': version, 'new_migrations': [], 'workflows': workflows,
    'model': {'provider': 'local llama.cpp CPU OpenAI-compatible API', 'name': private['modelConnection']['model'], 'model_id': private['modelId']},
    'financial_execution_enabled': False, 'file_count': len(files),
    'known_limits': ['Small-model reliability has not been statistically assessed; malformed outputs fail closed.',
                     'Native Plugin wrappers and standalone Agent entities are configuration references, not published deployments.',
                     'Billing/Subscription/Savings qualitative narration does not have the Lifestyle finding-selector semantic guarantee.',
                     'Six optional Docker integration tests were skipped; real P3 Docker execution and restart were tested separately.',
                     'P4 FastAPI/PostgreSQL service and P5-P7 are not implemented.'],
}
(out / 'acceptance.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({k: report[k] for k in ('unit', 'integration', 'container', 'file_count')}, ensure_ascii=False))
