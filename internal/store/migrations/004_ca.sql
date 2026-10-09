-- Each environment has its own TLS CA, so intercepted certificates never validate across
-- environments. The private key is sealed by the vault.
CREATE TABLE environment_cas (
    environment_id TEXT PRIMARY KEY REFERENCES environments(id) ON DELETE CASCADE,
    cert           BLOB NOT NULL,          -- DER
    sealed_key     BLOB NOT NULL,          -- PKCS #8, sealed with the environment ID as AAD
    created_at     INTEGER NOT NULL
);
