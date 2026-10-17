-- The migration job owns these objects. banking_agent_app is only a tenant client.
DO $$
BEGIN
    IF current_user = 'banking_agent_app' THEN
        RAISE EXCEPTION 'The runtime role cannot run banking-agent migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'banking_agent_app') THEN
        RAISE EXCEPTION 'Create the banking_agent_app runtime role before migrating';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'banking_agent_app'
               AND (rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole OR rolreplication)) THEN
        RAISE EXCEPTION 'banking_agent_app must be an unprivileged login role';
    END IF;
END
$$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM banking_agent_app;
GRANT USAGE ON SCHEMA public TO banking_agent_app;

CREATE TABLE public.agent_identities (
    owner_id text PRIMARY KEY CHECK (owner_id <> '' AND owner_id = btrim(owner_id)),
    banking_user_id text NOT NULL UNIQUE CHECK (banking_user_id = owner_id),
    coze_user_id text UNIQUE CHECK (coze_user_id IS NULL OR coze_user_id <> ''),
    trace_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE public.agent_financial_profiles (
    owner_id text PRIMARY KEY REFERENCES public.agent_identities(owner_id),
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    timezone text NOT NULL DEFAULT 'Asia/Shanghai' CHECK (timezone <> ''),
    safety_reserve_minor bigint NOT NULL DEFAULT 0 CHECK (safety_reserve_minor BETWEEN 0 AND 1000000000000),
    revision bigint NOT NULL CHECK (revision > 0),
    trace_id text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE public.agent_projection_resources (
    owner_id text NOT NULL REFERENCES public.agent_identities(owner_id),
    kind text NOT NULL CHECK (kind IN (
        'account', 'transaction', 'subscription', 'budget', 'lifestyle_goal',
        'lifestyle_plan', 'recommendation', 'operation'
    )),
    source_id text NOT NULL CHECK (source_id <> ''),
    source_version bigint NOT NULL CHECK (source_version > 0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'array'),
    audit_id text,
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    source_digest text NOT NULL CHECK (source_digest ~ '^[0-9a-f]{64}$'),
    trace_id text NOT NULL DEFAULT '',
    PRIMARY KEY (owner_id, kind, source_id)
);

CREATE TABLE public.agent_projection_receipts (
    owner_id text NOT NULL REFERENCES public.agent_identities(owner_id),
    event_id text NOT NULL CHECK (event_id <> ''),
    request_digest text NOT NULL CHECK (request_digest <> ''),
    result jsonb NOT NULL CHECK (jsonb_typeof(result) = 'object'),
    trace_id text NOT NULL DEFAULT '',
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (owner_id, event_id)
);

CREATE INDEX agent_projection_resources_recent
    ON public.agent_projection_resources(owner_id, kind, observed_at DESC, source_id);

ALTER TABLE public.agent_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_identities FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_identities_tenant ON public.agent_identities
    TO banking_agent_app
    USING (owner_id = nullif(current_setting('app.user_id', true), ''))
    WITH CHECK (owner_id = nullif(current_setting('app.user_id', true), ''));

ALTER TABLE public.agent_financial_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_financial_profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_financial_profiles_tenant ON public.agent_financial_profiles
    TO banking_agent_app
    USING (owner_id = nullif(current_setting('app.user_id', true), ''))
    WITH CHECK (owner_id = nullif(current_setting('app.user_id', true), ''));

ALTER TABLE public.agent_projection_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_projection_resources FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_projection_resources_tenant ON public.agent_projection_resources
    TO banking_agent_app
    USING (owner_id = nullif(current_setting('app.user_id', true), ''))
    WITH CHECK (owner_id = nullif(current_setting('app.user_id', true), ''));

ALTER TABLE public.agent_projection_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_projection_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_projection_receipts_tenant ON public.agent_projection_receipts
    TO banking_agent_app
    USING (owner_id = nullif(current_setting('app.user_id', true), ''))
    WITH CHECK (owner_id = nullif(current_setting('app.user_id', true), ''));

REVOKE ALL ON TABLE public.agent_identities, public.agent_financial_profiles,
    public.agent_projection_resources, public.agent_projection_receipts FROM PUBLIC;
REVOKE ALL ON TABLE public.agent_identities, public.agent_financial_profiles,
    public.agent_projection_resources, public.agent_projection_receipts FROM banking_agent_app;

GRANT SELECT, INSERT ON public.agent_identities TO banking_agent_app;
GRANT UPDATE (coze_user_id, trace_id) ON public.agent_identities TO banking_agent_app;
GRANT SELECT, INSERT ON public.agent_financial_profiles TO banking_agent_app;
GRANT UPDATE (timezone, safety_reserve_minor, revision, trace_id, updated_at)
    ON public.agent_financial_profiles TO banking_agent_app;
GRANT SELECT, INSERT ON public.agent_projection_resources TO banking_agent_app;
GRANT UPDATE (source_version, payload, evidence, audit_id, observed_at, source_digest, trace_id)
    ON public.agent_projection_resources TO banking_agent_app;
GRANT SELECT, INSERT ON public.agent_projection_receipts TO banking_agent_app;
GRANT SELECT ON public.agent_schema_migrations TO banking_agent_app;
