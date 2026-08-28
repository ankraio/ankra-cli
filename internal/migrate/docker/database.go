package docker

import (
	"strings"

	"ankra/internal/migrate"
)

// Image names, judged by the last path segment of the reference, that run a
// database server. The prefixes cover the official images and the common
// derivatives (postgis, pgvector, timescaledb, bitnami, percona); the
// exclusions stop tooling built around a database - exporters, admin UIs,
// connection poolers, PostgREST - from being dumped as one.
var (
	postgresImagePrefixes = []string{"postgres", "postgis", "pgvector", "pgvecto", "timescaledb"}
	mysqlImagePrefixes    = []string{"mysql", "mariadb", "percona"}
	notADatabaseFragments = []string{"exporter", "admin", "proxy", "bouncer", "rest", "backup", "operator", "client", "gui", "agent"}
)

// DatabaseEngine reports which database engine an image runs, if any.
func DatabaseEngine(image string) (string, bool) {
	name := imageName(image)
	if name == "" {
		return "", false
	}
	for _, fragment := range notADatabaseFragments {
		if strings.Contains(name, fragment) {
			return "", false
		}
	}
	for _, prefix := range postgresImagePrefixes {
		if strings.HasPrefix(name, prefix) {
			return migrate.EnginePostgres, true
		}
	}
	for _, prefix := range mysqlImagePrefixes {
		if strings.HasPrefix(name, prefix) {
			return migrate.EngineMySQL, true
		}
	}
	return "", false
}

// DefaultDatabasePort is the port an engine listens on unless configured
// otherwise: the port convert exposes on the database's Service, and the
// one a restore connects to.
func DefaultDatabasePort(engine string) int {
	switch engine {
	case migrate.EnginePostgres:
		return 5432
	case migrate.EngineMySQL:
		return 3306
	}
	return 0
}

// imageName returns the last path segment of an image reference without its
// tag or digest: "ghcr.io/org/pgvector:16" is "pgvector".
func imageName(image string) string {
	image = strings.ToLower(strings.TrimSpace(image))
	if at := strings.Index(image, "@"); at >= 0 {
		image = image[:at]
	}
	if slash := strings.LastIndex(image, "/"); slash >= 0 {
		image = image[slash+1:]
	}
	if colon := strings.Index(image, ":"); colon >= 0 {
		image = image[:colon]
	}
	return image
}
