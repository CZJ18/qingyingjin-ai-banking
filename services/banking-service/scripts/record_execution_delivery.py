"""Record real module8 verification artifacts; never read deployment secrets."""
import hashlib
import json
import subprocess
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

service = Path(__file__).resolve().parents[1]
root = service.parents[1]
evidence = service / 'output/module8'
docs = root / 'docs'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def result(name):
    events = [json.loads(line) for line in (evidence / name).read_text(encoding='utf-8-sig').splitlines() if line.strip()]
    failed = [event for event in events if event['Action'] == 'fail']
    packages = [event for event in events if event['Action'] == 'pass' and not event.get('Test')]
    assert packages and not failed, f'Incomplete or failed verification: {name}'
    return {
        'source': (evidence / name).relative_to(root).as_posix(),
        'pass_records': len([event for event in events if event['Action'] == 'pass' and event.get('Test')]),
        'packages_passed': len(packages),
        'skipped_records': len([event for event in events if event['Action'] == 'skip' and event.get('Test')]),
        'failures': len(failed),
        'finished_at': packages[-1]['Time'],
    }


now = datetime.now(timezone.utc).isoformat()
tests = {name: result(file) for name, file in {
    'unit': 'unit-tests.jsonl', 'integration_regression': 'integration-tests.jsonl',
    'security_after_expiry_audit_fix': 'security-tests.jsonl', 'planning': 'planning-tests.jsonl',
    'live_docker': 'docker-tests.jsonl', 'live_docker_after_restart': 'restart-tests.jsonl',
}.items()}
tests.update(date=now, note='Pass records include parent/subtests; integration repeats unit tests. Do not sum suites as independent cases. Conditional skips are not passes.', vet='passed; empty successful output in output/module8/vet.log', go_mod_verify='all modules verified', coze_source_unchanged=all(item['matches'] for item in json.loads((evidence / 'coze-source-checks.json').read_text(encoding='utf-8-sig'))))
(docs / 'banking-module8-test-results.json').write_text(json.dumps(tests, ensure_ascii=False, indent=2), encoding='utf-8')

containers = []
for name in ['qingyingjin-banking-service-1', 'qingyingjin-banking-mysql-1', 'qingyingjin-banking-redis-1']:
    raw = subprocess.check_output(['docker', 'inspect', '--format', '{{json .Name}} {{json .Image}} {{json .State.Status}} {{json .State.Health.Status}}', name], text=True).strip()
    fields = raw.split()
    containers.append(dict(name=json.loads(fields[0]), image=json.loads(fields[1]), state=json.loads(fields[2]), health=json.loads(fields[3])))
health = {'date': now, 'schema_version_required': 7, 'simulation_only': True, 'containers': containers}
for endpoint in ['healthz', 'readyz']:
    with urllib.request.urlopen('http://127.0.0.1:8090/' + endpoint, timeout=5) as response:
        health[endpoint] = dict(status=response.status, body=json.load(response))
        assert response.status == 200
for document in ['openapi.yaml', 'coze-plugin.openapi.yaml']:
    with urllib.request.urlopen('http://127.0.0.1:8090/' + document, timeout=5) as response:
        live = hashlib.sha256(response.read()).hexdigest()
    actual = digest(service / 'api' / document)
    assert live == actual, 'Runtime embedded contract differs from source'
    health[document] = {'runtime_sha256': live, 'source_sha256': actual, 'matches': True}
assert all(container['health'] == 'healthy' for container in containers)
health['restart_state'] = json.loads((evidence / 'docker-state.json').read_text())
(docs / 'banking-module8-health.json').write_text(json.dumps(health, ensure_ascii=False, indent=2), encoding='utf-8')

paths = [
    'docs/banking-module8-delivery.md', 'docs/superpowers/specs/2026-10-09-trusted-execution-design.md',
    'docs/superpowers/plans/2026-10-09-trusted-execution.md',
    'deploy/banking/compose.yaml', 'deploy/banking/Build-Image.ps1', 'deploy/banking/README.md',
    'services/banking-service/README.md', 'services/banking-service/MODULE8.md',
    'services/banking-service/cmd/server/main.go', 'services/banking-service/cmd/migrate/main.go',
    'services/banking-service/internal/entity/execution.go', 'services/banking-service/internal/entity/execution_test.go',
    'services/banking-service/internal/migration/007_execution.sql', 'services/banking-service/internal/migration/migration.go',
    'services/banking-service/internal/identity/identity.go', 'services/banking-service/internal/identity/identity_test.go',
    'services/banking-service/internal/risk/trusted.go', 'services/banking-service/internal/risk/trusted_test.go',
    'services/banking-service/internal/service/core.go', 'services/banking-service/internal/service/budget.go',
    'services/banking-service/internal/service/savings.go', 'services/banking-service/internal/service/transfer.go',
    'services/banking-service/internal/subscription/service.go', 'services/banking-service/internal/planning/execution.go',
    'services/banking-service/internal/httpapi/execution.go', 'services/banking-service/internal/httpapi/execution_test.go',
    'services/banking-service/internal/httpapi/server.go', 'services/banking-service/internal/httpapi/contract_test.go',
    'services/banking-service/internal/orchestrator/executor.go', 'services/banking-service/internal/orchestrator/model.go',
    'services/banking-service/internal/orchestrator/scheduler.go',
    'services/banking-service/api/openapi.yaml', 'services/banking-service/api/coze-plugin.openapi.yaml',
    'services/banking-service/api/openapi_test.go', 'services/banking-service/scripts/generate_core_openapi.py',
    'services/banking-service/scripts/generate_execution_openapi.py', 'services/banking-service/scripts/record_execution_delivery.py',
]
paths.extend(path.relative_to(root).as_posix() for path in (service / 'internal/execution').glob('*.go'))
paths.extend(path.relative_to(root).as_posix() for path in (service / 'tests/integration').glob('execution*_test.go'))
paths.extend(path.relative_to(root).as_posix() for path in (root / 'coze/banking/execution-gateway').rglob('*') if path.is_file())
paths.extend(path.relative_to(root).as_posix() for path in (root / 'coze/banking').rglob('*.yaml') if path.name in ['plugin.openapi.yaml', 'coze-plugin.openapi.yaml'])
manifest = {'date': now, 'note': 'Explicit source/config/docs manifest; no usable Git HEAD. Credentials, binaries, pycache and test evidence excluded. Health and test records saved separately.', 'files': [{'path': path, 'sha256': digest(root / path)} for path in sorted(set(paths))]}
(docs / 'banking-module8-files.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps({'files': len(manifest['files']), 'unit_pass_records': tests['unit']['pass_records'], 'integration_pass_records': tests['integration_regression']['pass_records'], 'skips': tests['integration_regression']['skipped_records'], 'healthy_containers': len(containers), 'coze_source_unchanged': tests['coze_source_unchanged']}))
