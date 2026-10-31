-- A non-login metadata-only role owns the fixed queue scan. It cannot read
-- requests, results, checkpoints, or the encrypted Go task capability.
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='banking_agent_scheduler') THEN
  CREATE ROLE banking_agent_scheduler NOLOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='banking_agent_scheduler'
 AND (rolcanlogin OR rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole)) THEN
  RAISE EXCEPTION 'scheduler metadata role must remain unprivileged and non-login';
 END IF;
END $$;
ALTER TABLE public.agent_tasks ADD COLUMN background_attempts integer NOT NULL DEFAULT 0 CHECK(background_attempts BETWEEN 0 AND 3);
ALTER TABLE public.agent_tasks ADD COLUMN next_run_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE TABLE public.agent_background_credentials (
 owner_id text NOT NULL, task_id text NOT NULL,
 capability text NOT NULL CHECK(length(capability) BETWEEN 16 AND 8192),
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(owner_id,task_id),
 FOREIGN KEY(owner_id,task_id) REFERENCES public.agent_tasks(owner_id,task_id)
);
ALTER TABLE public.agent_background_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_background_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON public.agent_background_credentials TO banking_agent_app
 USING(owner_id=nullif(current_setting('app.user_id',true),''))
 WITH CHECK(owner_id=nullif(current_setting('app.user_id',true),''));
REVOKE ALL ON public.agent_background_credentials FROM PUBLIC, banking_agent_app;
GRANT SELECT,INSERT ON public.agent_background_credentials TO banking_agent_app;
GRANT UPDATE(capability,expires_at) ON public.agent_background_credentials TO banking_agent_app;
GRANT UPDATE(background_attempts,next_run_at) ON public.agent_tasks TO banking_agent_app;
GRANT USAGE ON SCHEMA public TO banking_agent_scheduler;
GRANT SELECT(owner_id,task_id,status,lease_until,next_run_at,background_attempts) ON public.agent_tasks TO banking_agent_scheduler;
GRANT SELECT(owner_id,task_id,expires_at) ON public.agent_background_credentials TO banking_agent_scheduler;
CREATE POLICY scheduler_metadata ON public.agent_tasks FOR SELECT TO banking_agent_scheduler USING(true);
CREATE POLICY scheduler_metadata ON public.agent_background_credentials FOR SELECT TO banking_agent_scheduler USING(true);
CREATE FUNCTION public.agent_background_candidates() RETURNS TABLE(owner_id text, task_id text)
 LANGUAGE sql SECURITY DEFINER SET search_path='' AS $$
 SELECT t.owner_id,t.task_id FROM public.agent_tasks t
 JOIN public.agent_background_credentials c ON c.owner_id=t.owner_id AND c.task_id=t.task_id
 WHERE t.next_run_at<=pg_catalog.clock_timestamp()
 AND ((t.status='queued' AND t.background_attempts<3) OR (t.status='running' AND t.lease_until<=pg_catalog.clock_timestamp()))
 ORDER BY t.next_run_at,t.task_id LIMIT 32
$$;
ALTER FUNCTION public.agent_background_candidates() OWNER TO banking_agent_scheduler;
REVOKE ALL ON FUNCTION public.agent_background_candidates() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.agent_background_candidates() TO banking_agent_app;
