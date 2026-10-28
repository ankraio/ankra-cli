package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"ankra/internal/migrate"
)

// dataImages are images that hold data this module knows it does not dump.
// Naming them is the difference between "migrated" and "migrated, except
// the search index".
var dataImages = map[string]string{
	"mongo":         "a MongoDB database",
	"mongodb":       "a MongoDB database",
	"redis":         "a Redis dataset",
	"valkey":        "a Valkey dataset",
	"elasticsearch": "an Elasticsearch index",
	"opensearch":    "an OpenSearch index",
	"rabbitmq":      "RabbitMQ queues",
	"minio":         "MinIO object storage",
	"clickhouse":    "a ClickHouse database",
	"influxdb":      "an InfluxDB database",
	"neo4j":         "a Neo4j graph",
	"cassandra":     "a Cassandra keyspace",
	"couchdb":       "a CouchDB database",
}

// PlanExport implements migrate.ExportPlanner: it resolves every database
// service to its container, asks each server what it holds and how big it
// is, and lists the data the source keeps that the export will not carry.
// Nothing is written.
func (module Module) PlanExport(ctx context.Context, request migrate.ExportRequest) (migrate.ExportPlan, error) {
	source, err := module.discoverSource(ctx, request)
	if err != nil {
		return migrate.ExportPlan{}, err
	}
	plan := migrate.ExportPlan{Warnings: append([]string(nil), source.warnings...)}
	for _, server := range source.servers {
		job := dumpJob{docker: source.docker, server: server, only: splitList(source.options[OptionDatabasesPrefix+server.workload.Name])}
		planned, warnings, err := planServer(ctx, job)
		if err != nil {
			return migrate.ExportPlan{}, err
		}
		plan.Databases = append(plan.Databases, planned)
		plan.Warnings = append(plan.Warnings, warnings...)
	}
	plan.NotCarried = notCarried(source)
	plan.Warnings = uniqueSorted(plan.Warnings)
	return plan, nil
}

// planServer reads one server's version and databases with their sizes.
func planServer(ctx context.Context, job dumpJob) (migrate.PlannedDatabaseServer, []string, error) {
	workload := job.server.workload.Name
	planned := migrate.PlannedDatabaseServer{Workload: workload, Engine: job.server.engine, Container: job.server.container}
	var warnings []string
	var client func(string) []string
	var listed []databaseSize
	switch job.server.engine {
	case migrate.EnginePostgres:
		username, applicationDatabase := postgresCredentials(job.server)
		planned.Username = username
		client = postgresClient(job, username)
		version, err := job.docker.Output(ctx, client("SHOW server_version")...)
		if err != nil {
			return migrate.PlannedDatabaseServer{}, nil, fmt.Errorf("%s: reading the server version: %w", workload, err)
		}
		planned.ServerVersion = strings.TrimSpace(string(version))
		databases, listWarnings, err := listPostgresDatabases(ctx, job.docker, client, applicationDatabase, workload)
		if err != nil {
			return migrate.PlannedDatabaseServer{}, nil, err
		}
		listed, warnings = databases, listWarnings
	case migrate.EngineMySQL:
		username, passwordKey, credentialWarning, err := mysqlCredentials(job.server)
		if err != nil {
			return migrate.PlannedDatabaseServer{}, nil, err
		}
		if credentialWarning != "" {
			warnings = append(warnings, credentialWarning)
		}
		planned.Username = username
		client = mysqlClient(job, username, passwordKey)
		version, err := job.docker.Output(ctx, client("SELECT VERSION()")...)
		if err != nil {
			return migrate.PlannedDatabaseServer{}, nil, fmt.Errorf("%s: reading the server version: %w", workload, err)
		}
		planned.ServerVersion = strings.TrimSpace(string(version))
		listed, err = listMySQLDatabases(ctx, job.docker, client, workload)
		if err != nil {
			return migrate.PlannedDatabaseServer{}, nil, err
		}
	}
	only := map[string]bool{}
	for _, name := range job.only {
		only[name] = true
	}
	for _, database := range listed {
		if len(only) > 0 && !only[database.name] {
			continue
		}
		planned.Databases = append(planned.Databases, migrate.PlannedDatabase{Name: database.name, SizeBytes: database.sizeBytes})
	}
	for name := range only {
		isListed := false
		for _, database := range planned.Databases {
			isListed = isListed || database.Name == name
		}
		if !isListed {
			planned.Databases = append(planned.Databases, migrate.PlannedDatabase{Name: name})
		}
	}
	if len(planned.Databases) == 0 {
		return migrate.PlannedDatabaseServer{}, nil, fmt.Errorf("%s: the server holds no database to carry", workload)
	}
	return planned, warnings, nil
}

// notCarried lists what the source holds beyond its database servers:
// files in the volumes of the other workloads, and data engines this
// module does not dump.
func notCarried(source exportSource) []string {
	var items []string
	for _, workload := range source.others {
		if description, ok := dataImage(workload.Image); ok {
			items = append(items, fmt.Sprintf("%s runs %s (%s), which this export does not dump", workload.Name, description, imageName(workload.Image)))
		}
		var mounts []string
		for _, volume := range workload.Volumes {
			if volume.ReadOnly || volume.BindFile {
				continue
			}
			mounts = append(mounts, volume.Target)
		}
		if len(mounts) > 0 {
			sort.Strings(mounts)
			items = append(items, fmt.Sprintf("%s keeps files in %s; volumes are not carried by this export - copy them into the cluster's PersistentVolumeClaim yourself", workload.Name, strings.Join(mounts, ", ")))
		}
	}
	return items
}

// dataImage recognises an image that holds application data of a kind this
// module does not dump.
func dataImage(image string) (string, bool) {
	name := imageName(image)
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	description, ok := dataImages[name]
	return description, ok
}

// QuiesceSource implements migrate.SourceQuiescer: it stops every container
// of the compose project that is not a database server, so the export that
// follows is the last word on the data. The databases keep running - the
// dump needs them - and the command to bring the rest back is returned.
func (module Module) QuiesceSource(ctx context.Context, request migrate.ExportRequest) (migrate.Quiesce, error) {
	source, err := module.discoverSource(ctx, request)
	if err != nil {
		return migrate.Quiesce{}, err
	}
	composeProject := source.options[OptionProject]
	if composeProject == "" {
		composeProject = composeProjectName(source.project.Name)
	}
	var quiesce migrate.Quiesce
	var containers []string
	for _, workload := range source.others {
		output, err := source.docker.Output(ctx, "ps", "-q",
			"--filter", "label="+labelComposeProject+"="+composeProject,
			"--filter", "label="+labelComposeService+"="+workload.Name)
		if err != nil {
			return migrate.Quiesce{}, err
		}
		ids := strings.Fields(string(output))
		if len(ids) == 0 {
			continue
		}
		containers = append(containers, ids...)
		quiesce.Stopped = append(quiesce.Stopped, workload.Name)
	}
	if len(containers) == 0 {
		return quiesce, nil
	}
	if _, err := source.docker.Output(ctx, append([]string{"stop"}, containers...)...); err != nil {
		return migrate.Quiesce{}, fmt.Errorf("stopping the source's writers: %w", err)
	}
	quiesce.Resume = "docker start " + strings.Join(containers, " ")
	return quiesce, nil
}
