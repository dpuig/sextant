-- One-time agent registration tokens. Only a SHA-256 of the secret is stored.
CREATE TABLE registration_tokens (
  tenant_id  text        NOT NULL,
  token_hash text        NOT NULL,
  agent_name text        NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, token_hash)
);

ALTER TABLE registration_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE registration_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON registration_tokens
  USING (tenant_id = current_setting('sextant.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('sextant.tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON registration_tokens TO sextant_app;
