package main

func (a *App) migrate() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, color TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS profiles (id INTEGER PRIMARY KEY, display_name TEXT NOT NULL, phone TEXT, email TEXT, consent INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS source_customers (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL REFERENCES tenants(id), external_id TEXT NOT NULL, profile_id INTEGER NOT NULL REFERENCES profiles(id), name TEXT, phone TEXT, email TEXT, UNIQUE(tenant_id,external_id))`,
		`CREATE TABLE IF NOT EXISTS identity_decisions (source_customer_id INTEGER PRIMARY KEY REFERENCES source_customers(id), profile_id INTEGER NOT NULL REFERENCES profiles(id), method TEXT NOT NULL, confidence REAL NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_source_phone ON source_customers(phone)`,
		`CREATE INDEX IF NOT EXISTS idx_source_email ON source_customers(email)`,
		`CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL REFERENCES tenants(id), profile_id INTEGER NOT NULL REFERENCES profiles(id), external_id TEXT NOT NULL, kind TEXT NOT NULL, occurred_at TEXT NOT NULL, amount REAL NOT NULL DEFAULT 0, related_external_id TEXT, source TEXT NOT NULL, imported_at TEXT NOT NULL, UNIQUE(tenant_id,external_id))`,
		`CREATE INDEX IF NOT EXISTS idx_events_profile ON events(profile_id,occurred_at)`,
		`CREATE TABLE IF NOT EXISTS imports (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL REFERENCES tenants(id), filename TEXT NOT NULL, rows_total INTEGER NOT NULL, rows_ok INTEGER NOT NULL, rows_failed INTEGER NOT NULL, errors TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS campaigns (id INTEGER PRIMARY KEY, name TEXT NOT NULL, source_tenant_id INTEGER NOT NULL REFERENCES tenants(id), target_tenant_id INTEGER NOT NULL REFERENCES tenants(id), created_at TEXT NOT NULL, window_days INTEGER NOT NULL DEFAULT 14)`,
		`CREATE TABLE IF NOT EXISTS campaign_members (campaign_id INTEGER NOT NULL REFERENCES campaigns(id), profile_id INTEGER NOT NULL REFERENCES profiles(id), PRIMARY KEY(campaign_id,profile_id))`,
		`CREATE TABLE IF NOT EXISTS connectors (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL REFERENCES tenants(id), name TEXT NOT NULL, url TEXT NOT NULL, array_path TEXT NOT NULL, field_map TEXT NOT NULL, auth_header TEXT NOT NULL DEFAULT '', auth_value TEXT NOT NULL DEFAULT '', last_sync_at TEXT NOT NULL DEFAULT '', last_result TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS demo_seed_state (version INTEGER PRIMARY KEY, seeded_at TEXT NOT NULL)`,
	}
	for _, s := range schema {
		if _, e := a.db.Exec(s); e != nil {
			return e
		}
	}
	return nil
}
