CREATE TABLE public.agent_tasks (
 owner_id text NOT NULL REFERENCES public.agent_identities(owner_id),
 task_id text NOT NULL CHECK(task_id ~ '^lg-[0-9a-f-]{36}$'),
 owner_engine text NOT NULL DEFAULT 'langgraph' CHECK(owner_engine='langgraph'),
 idempotency_key text NOT NULL, request_digest text NOT NULL,
 request jsonb NOT NULL CHECK(octet_length(request::text)<=1048576),
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','interrupted','waiting_confirmation','succeeded','failed','cancelled')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 lease_token text, lease_until timestamptz,
 result jsonb CHECK(octet_length(result::text)<=1048576), error_code text NOT NULL DEFAULT '',
 trace_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,task_id), UNIQUE(owner_id,idempotency_key)
);
CREATE TABLE public.agent_tool_calls (
 owner_id text NOT NULL, task_id text NOT NULL, node_key text NOT NULL,
 request_digest text NOT NULL, mode text NOT NULL, state text NOT NULL CHECK(state IN ('running','succeeded','failed','unknown')),
 result jsonb CHECK(octet_length(result::text)<=1048576), error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,task_id,node_key), FOREIGN KEY(owner_id,task_id) REFERENCES public.agent_tasks(owner_id,task_id)
);
CREATE TABLE public.agent_confirmations (
 owner_id text NOT NULL, task_id text NOT NULL, node_key text NOT NULL, digest text NOT NULL,
 nonce text NOT NULL, parameters_view jsonb NOT NULL CHECK(octet_length(parameters_view::text)<=1048576),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected')),
 waiting_version bigint NOT NULL, expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), confirmed_at timestamptz,
 PRIMARY KEY(owner_id,task_id,node_key), FOREIGN KEY(owner_id,task_id) REFERENCES public.agent_tasks(owner_id,task_id)
);
CREATE TABLE public.agent_task_events (
 owner_id text NOT NULL, task_id text NOT NULL, seq bigint NOT NULL,
 status text NOT NULL, node_key text NOT NULL DEFAULT '', error_code text NOT NULL DEFAULT '', trace_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,task_id,seq), FOREIGN KEY(owner_id,task_id) REFERENCES public.agent_tasks(owner_id,task_id)
);
CREATE UNIQUE INDEX agent_confirmations_one_pending ON public.agent_confirmations(owner_id,task_id) WHERE status='pending';
CREATE TABLE public.agent_checkpoints (
 owner_id text NOT NULL, task_id text NOT NULL, checkpoint_ns text NOT NULL,
 checkpoint_id text NOT NULL, parent_id text, checkpoint_type text NOT NULL,
 checkpoint bytea NOT NULL CHECK(octet_length(checkpoint)<=1048576),
 metadata jsonb NOT NULL CHECK(octet_length(metadata::text)<=1048576),
 PRIMARY KEY(owner_id,task_id,checkpoint_ns,checkpoint_id), FOREIGN KEY(owner_id,task_id) REFERENCES public.agent_tasks(owner_id,task_id)
);
CREATE TABLE public.agent_checkpoint_writes (
 owner_id text NOT NULL, task_id text NOT NULL, checkpoint_ns text NOT NULL,
 checkpoint_id text NOT NULL, write_task_id text NOT NULL, idx integer NOT NULL,
 channel text NOT NULL, value_type text NOT NULL, value bytea NOT NULL CHECK(octet_length(value)<=1048576), task_path text NOT NULL,
 PRIMARY KEY(owner_id,task_id,checkpoint_ns,checkpoint_id,write_task_id,idx),
 FOREIGN KEY(owner_id,task_id) REFERENCES public.agent_tasks(owner_id,task_id)
);
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['agent_tasks','agent_tool_calls','agent_confirmations','agent_task_events','agent_checkpoints','agent_checkpoint_writes'] LOOP
  EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON public.%I USING(owner_id = nullif(current_setting(''app.user_id'',true),'''')) WITH CHECK(owner_id = nullif(current_setting(''app.user_id'',true),''''))',tab);
  EXECUTE format('REVOKE ALL ON public.%I FROM PUBLIC, banking_agent_app',tab);
  EXECUTE format('GRANT SELECT, INSERT ON public.%I TO banking_agent_app',tab);
 END LOOP;
END $$;
GRANT UPDATE(status,version,lease_token,lease_until,result,error_code,updated_at) ON public.agent_tasks TO banking_agent_app;
GRANT UPDATE(state,result,error_code,updated_at) ON public.agent_tool_calls TO banking_agent_app;
GRANT UPDATE(status,confirmed_at,waiting_version) ON public.agent_confirmations TO banking_agent_app;
GRANT UPDATE(checkpoint_type,checkpoint,metadata,parent_id) ON public.agent_checkpoints TO banking_agent_app;
GRANT UPDATE(channel,value_type,value,task_path) ON public.agent_checkpoint_writes TO banking_agent_app;
