"""Read-only public projection must expose durable uncertainty, not offer replay."""
from copy import deepcopy
from datetime import datetime, UTC
import pytest
from banking_agent.task_repository import TaskRepository

class Rows:
    def __init__(self,value):self.value=value
    def fetchone(self):return self.value
class Connection:
    def __init__(self,unsafe):self.unsafe=unsafe;self.queries=[]
    def execute(self,sql,args):
        self.queries.append((sql,args))
        if 'agent_tool_calls' in sql:return Rows(self.unsafe)
        if 'agent_confirmations' in sql:return Rows(None)
        raise AssertionError('Unexpected SQL')

@pytest.mark.parametrize('node,expected',[('__internal:supervisor_plan','MODEL_OUTCOME_UNKNOWN'),('billing','TOOL_OUTCOME_UNKNOWN')])
def test_durable_unknown_is_visible_without_rewriting_history(node,expected):
    row=dict(task_id='lg-00000000-0000-0000-0000-000000000001',owner_id='synthetic-42',owner_engine='langgraph',
        status='failed',version=3,request={},result=None,error_code='COZE_UNAVAILABLE',created_at=datetime.now(UTC),updated_at=datetime.now(UTC))
    before=deepcopy(row);conn=Connection(dict(node_key=node,state='unknown',error_code='COZE_UNAVAILABLE'))
    view=TaskRepository._view(conn,row)
    assert view['error_code']==expected
    assert row==before
    assert all(sql.startswith('SELECT ') and args[:2]==('synthetic-42',row['task_id']) for sql,args in conn.queries)

def test_known_failed_task_keeps_original_error():
    row=dict(task_id='lg-00000000-0000-0000-0000-000000000001',owner_id='synthetic-42',owner_engine='langgraph',
        status='failed',version=3,request={},result=None,error_code='INVALID_TASK_PLAN',created_at=datetime.now(UTC),updated_at=datetime.now(UTC))
    assert TaskRepository._view(Connection(None),row)['error_code']=='INVALID_TASK_PLAN'
