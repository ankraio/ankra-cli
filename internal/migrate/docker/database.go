package docker

import (
	"path"
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

// databaseVolumeSubPath is the subdirectory of the claim a database's data
// directory is mounted from.
const databaseVolumeSubPath = "data"

// mysqlDataDirectoryFlag moves a mysql or mariadb data directory on the
// command line, the way PGDATA moves postgres's.
const mysqlDataDirectoryFlag = "--datadir="

// databaseMountSubPath reports the subdirectory of a named volume a database
// workload's data directory must be mounted from, and "" when the mount can
// stay exactly as the source wrote it.
//
// Every ext4 block volume - Hetzner, UpCloud, DigitalOcean and the rest of
// the CSI drivers that format a fresh disk - carries a lost+found directory
// at its root, and a database refuses to initialise into a directory that is
// not empty: postgres's initdb fails outright, mariadb warns, mysql tolerates
// it. Compose mounts the volume straight onto the data directory, so the
// conversion mounts it one level down instead. subPath is the choice here
// because it is engine-agnostic: it keeps the path inside the container
// exactly where the image expects it, and works the same for postgres, mysql
// and mariadb, where injecting PGDATA would move postgres alone and leave
// every other engine to its own entrypoint's flags.
//
// A source that already writes below the mount - PGDATA or --datadir set to
// a subdirectory, the hand-rolled workaround for this very failure - is left
// untouched: the directory it names is already a fresh one, and a subPath on
// top would put the data somewhere the source never wrote.
func databaseMountSubPath(workload Workload, volume Volume) string {
	if !volume.Named || volume.ReadOnly {
		return ""
	}
	engine, isDatabase := DatabaseEngine(workload.Image)
	if !isDatabase {
		return ""
	}
	if path.Clean(volume.Target) != databaseDataDirectory(workload, engine) {
		return ""
	}
	return databaseVolumeSubPath
}

// databaseDataDirectory is the directory a database workload writes its files
// to: the one the source configured, when it configured one, and the image's
// default otherwise.
func databaseDataDirectory(workload Workload, engine string) string {
	configured := ""
	switch engine {
	case migrate.EnginePostgres:
		for _, entry := range workload.Env {
			if entry.Name == "PGDATA" {
				configured = entry.Value
			}
		}
		if configured == "" {
			return "/var/lib/postgresql/data"
		}
	case migrate.EngineMySQL:
		for _, argument := range append(append([]string{}, workload.Command...), workload.Args...) {
			if strings.HasPrefix(argument, mysqlDataDirectoryFlag) {
				configured = strings.TrimPrefix(argument, mysqlDataDirectoryFlag)
			}
		}
		if configured == "" {
			return "/var/lib/mysql"
		}
	}
	return path.Clean(configured)
}
