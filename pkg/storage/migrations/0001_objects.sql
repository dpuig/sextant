-- App role: no login by default, not the table owner, so RLS applies to it.
-- Deployments grant LOGIN and a password out of band.
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'sextant_app') THEN
    CREATE ROLE sextant_app NOLOGIN NOSUPERUSER NOBYPASSRLS;
  END IF;
END $$;

CREATE SEQUENCE resource_version_seq;

CREATE TABLE objects (
  tenant_id        text   NOT NULL,
  kind             text   NOT NULL,
  name             text   NOT NULL,
  resource_version bigint NOT NULL DEFAULT nextval('resource_version_seq'),
  spec             jsonb  NOT NULL DEFAULT '{}',
  status           jsonb  NOT NULL DEFAULT '{}',
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, kind, name)
);

ALTER TABLE objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE objects FORCE ROW LEVEL SECURITY;

-- Fail closed: with no tenant set, current_setting yields NULL and no row matches.
CREATE POLICY tenant_isolation ON objects
  USING (tenant_id = current_setting('sextant.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('sextant.tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON objects TO sextant_app;
GRANT USAGE ON SEQUENCE resource_version_seq TO sextant_app;
