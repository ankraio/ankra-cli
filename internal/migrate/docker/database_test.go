package docker

import (
	"testing"

	"ankra/internal/migrate"
)

func TestDatabaseEngine(t *testing.T) {
	cases := map[string]string{
		"postgres:17":                                  migrate.EnginePostgres,
		"postgres":                                     migrate.EnginePostgres,
		"docker.io/library/postgres:16-alpine":         migrate.EnginePostgres,
		"pgvector/pgvector:0.8.1-pg17":                 migrate.EnginePostgres,
		"postgis/postgis:16-3.4":                       migrate.EnginePostgres,
		"bitnami/postgresql:16":                        migrate.EnginePostgres,
		"timescale/timescaledb-ha:pg16":                migrate.EnginePostgres,
		"supabase/postgres:15.1":                       migrate.EnginePostgres,
		"localhost:5000/team/postgres@sha256:abcdef":   migrate.EnginePostgres,
		"mysql:8.4":                                    migrate.EngineMySQL,
		"mariadb:11":                                   migrate.EngineMySQL,
		"bitnami/mariadb:11":                           migrate.EngineMySQL,
		"percona/percona-server:8.0":                   migrate.EngineMySQL,
		"nginx:1.27":                                   "",
		"redis:7":                                      "",
		"prom/mysqld-exporter:v0.15":                   "",
		"postgrest/postgrest:v12":                      "",
		"dpage/pgadmin4:8":                             "",
		"edoburu/pgbouncer:1.22":                       "",
		"prometheuscommunity/postgres-exporter:v0.15":  "",
		"ghcr.io/cloudnative-pg/cloudnative-pg:1.24.0": "",
		"": "",
	}
	for image, want := range cases {
		engine, ok := DatabaseEngine(image)
		if engine != want || ok != (want != "") {
			t.Errorf("DatabaseEngine(%q) = %q %v, want %q", image, engine, ok, want)
		}
	}
	if DefaultDatabasePort(migrate.EnginePostgres) != 5432 || DefaultDatabasePort(migrate.EngineMySQL) != 3306 || DefaultDatabasePort("other") != 0 {
		t.Error("default ports")
	}
}
