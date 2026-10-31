"""Explicit isolated-runtime SQL acceptance; invoked inside the new Agent only."""
import json
import os
from uuid import uuid4

from banking_agent.repository import Repository
from banking_agent.task_repository import TaskRepository

database = os.environ['BANKING_AGENT_DATABASE_URL']
if '/banking_business_agent_20261022_test' not in database:
    raise RuntimeError('Requires the new isolated business database')
repository = Repository(database)
tasks = TaskRepository(repository)
user = 'business-history-' + str(uuid4())
other = user + '-other'
repository.ensure_identity(user, None, 'isolated-history-check')
repository.ensure_identity(other, None, 'isolated-history-check')
first = tasks.submit(user, 'history-1', 'first', {'instruction': 'First SQL task'}, 'history-check')
second = tasks.submit(user, 'history-2', 'second', {'instruction': 'Second SQL task'}, 'history-check')
foreign = tasks.submit(other, 'history-other', 'foreign', {'instruction': 'Foreign task'}, 'history-check')
events = tasks.events(user, first['task_id'], 0, 100)
page = tasks.list_recent(user, 1, 0)
assert page['total'] == 2
assert page['items'][0]['task_id'] == second['task_id']
assert page['items'][0]['instruction'] == 'Second SQL task'
assert tasks.list_recent(user, 1, 1)['items'][0]['task_id'] == first['task_id']
assert tasks.list_recent(user, 1, 2)['items'] == []
assert foreign['task_id'] not in [x['task_id'] for x in tasks.list_recent(user, 100)['items']]
assert set(page['items'][0]) == {'task_id', 'status', 'version', 'instruction', 'display_title', 'error_code', 'created_at', 'updated_at'}
assert tasks.get(user, first['task_id'])['status'] == 'queued'
assert tasks.events(user, first['task_id'], 0, 100) == events
with repository._transaction(user, readonly=True) as connection:
    assert connection.execute('SHOW transaction_read_only').fetchone()['transaction_read_only'] == 'on'
    assert connection.execute('SHOW transaction_isolation').fetchone()['transaction_isolation'] == 'repeatable read'
repository.close()
print(json.dumps({'status': 'PASS', 'checks': ['real SQL paging', 'owner isolation', 'summary field allowlist',
    'history leaves task/events unchanged', 'repeatable-read read-only transaction'],
    'supplier_requests': 0, 'financial_execution': 0}))
