"""Record actual P2 logs and intentionally touched files, no invented Git diff."""
from pathlib import Path
from collections import Counter
import hashlib
import json
import subprocess

root=Path(__file__).resolve().parents[3];out=root/'services/banking-service/output/module10'
def events(name):
 rows=[json.loads(x) for x in (out/name).read_text(encoding='utf-8-sig').splitlines() if x.strip()]
 tests=Counter(r['Action'] for r in rows if r.get('Test') and r['Action'] in ['pass','fail','skip'])
 packages=Counter(r['Action'] for r in rows if not r.get('Test') and r['Action'] in ['pass','fail'])
 return {'test_events':{k:tests[k] for k in ['pass','fail','skip']},'packages':dict(packages),'skipped_tests':[r['Test'] for r in rows if r.get('Test') and r['Action']=='skip']}
paths=[
 'services/banking-service/MODULE10.md',
 'services/banking-service/Dockerfile.runtime',
 'docs/superpowers/plans/2026-10-13-qingrongjin-lifestyle-p2.md',
 'docs/superpowers/plans/2026-10-13-qingrongjin-upgrade-roadmap.md',
 'services/banking-service/internal/billing/category.go',
 'services/banking-service/internal/goal/evidence.go',
 'services/banking-service/internal/planning/lifestyle.go',
 'services/banking-service/internal/httpapi/lifestyle.go',
 'services/banking-service/internal/httpapi/server.go',
 'services/banking-service/internal/httpapi/contract_test.go',
 'services/banking-service/internal/migration/migration.go',
 'services/banking-service/internal/migration/010_lifestyle.sql',
 'services/banking-service/internal/config/config.go',
 'services/banking-service/internal/identity/identity.go',
 'services/banking-service/internal/skills/registry.go',
 'services/banking-service/internal/skills/lifestyle_test.go',
 'services/banking-service/internal/orchestrator/graph.go',
 'services/banking-service/internal/orchestrator/executor.go',
 'services/banking-service/tests/integration/lifestyle_test.go',
 'services/banking-service/cmd/server/main.go',
 'services/banking-service/api/openapi.yaml',
 'services/banking-service/api/coze-plugin.openapi.yaml',
 'services/banking-service/api/openapi_test.go',
 'services/banking-service/scripts/generate_core_openapi.py',
 'services/banking-service/scripts/generate_lifestyle_openapi.py',
 'services/banking-service/scripts/verify_lifestyle_live.py',
 'services/banking-service/scripts/record_lifestyle_delivery.py',
 'coze/banking/supervisor-agent/agent-config.yaml',
 'coze/banking/supervisor-agent/system-prompt.md',
 'coze/banking/supervisor-agent/task-plan.schema.json',
 'coze/banking/supervisor-agent/explanation.schema.json',
 'deploy/banking/.env.example','deploy/banking/compose.yaml','deploy/banking/Build-Image.ps1']
paths += [p.relative_to(root).as_posix() for folder in ['services/banking-service/internal/lifestyle','coze/banking/lifestyle-agent'] for p in (root/folder).iterdir() if p.is_file()]
files=[{'path':str(root/p),'sha256':hashlib.sha256((root/p).read_bytes()).hexdigest()} for p in sorted(set(paths))]
(out/'files.json').write_text(json.dumps({'description':'P2 intentionally implemented/updated source files; current hashes, not Git diff','files':files},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
image=json.loads(subprocess.check_output(['docker','inspect','qingyingjin-banking-service-1'],encoding='utf-8'))[0]
assert int((out/'migration-version.txt').read_text(encoding='utf-8-sig').strip())==10
report={'build_vet':json.loads((out/'build-vet.json').read_text(encoding='utf-8-sig')),'unit':events('unit-final.jsonl'),'integration':events('integration-final.jsonl'),'race':(out/'race.log').read_text(encoding='utf-8-sig').strip(),'python':{'supervisor_tests':6,'lifestyle_tests':3,'logs':['supervisor-python.log','lifestyle-python.log']},'live':json.loads((out/'live.json').read_text(encoding='utf-8')),'restart':json.loads((out/'live-restart.json').read_text(encoding='utf-8')),'container':{'image':image['Config']['Image'],'image_id':image['Image'],'health':image['State'].get('Health',{}).get('Status')},'migration_version':10,'financial_operations_executed_by_p2':False,'v2_real_model_executed':False,'file_count':len(files),'known_limits':['New Coze v2 parser / Lifestyle explanation and upgraded Supervisor not yet published; P3 work','Savings inputs must be explicit current-month settings; forecasts are not cash'],'build_environment_note':'Docker daemon was restarted; gcr.io HEAD failed with EOF. Final build used verified existing nonroot runtime image qingyingjin-banking-runtime:module10-cache and newly compiled binaries.'}
(out/'acceptance.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps({k:report[k] for k in ['unit','integration','container','file_count']},ensure_ascii=False))
