package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMySQLProgramsPickTheBinaryTheImageShips runs the shell the export hands
// to `docker exec` against a PATH holding only the binaries one image family
// ships: MariaDB 11 images have no mysql/mysqldump (ankra-5m87sr), MySQL
// images have no mariadb-named tools, and older MariaDB images have both.
func TestMySQLProgramsPickTheBinaryTheImageShips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the export's in-container script is POSIX sh")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	job := dumpJob{server: databaseServer{container: "db"}}
	cases := []struct {
		name     string
		binaries []string
		wantDump string
		wantCli  string
	}{
		{"mariadb 11", []string{"mariadb", "mariadb-dump"}, "mariadb-dump", "mariadb"},
		{"mysql", []string{"mysql", "mysqldump"}, "mysqldump", "mysql"},
		{"older mariadb", []string{"mariadb", "mariadb-dump", "mysql", "mysqldump"}, "mariadb-dump", "mariadb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			for _, name := range tc.binaries {
				script := "#!" + shell + "\necho \"" + name + " pwd=$MYSQL_PWD $*\"\n"
				if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			run := func(args []string) string {
				// args is: exec <container> sh -c <script> sh <args...>
				command := exec.Command(shell, args[3:]...)
				command.Env = []string{"PATH=" + bin, "MARIADB_ROOT_PASSWORD=secret"}
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %v\n%s", args, err, output)
				}
				return strings.TrimSpace(string(output))
			}
			dump := run(job.inContainer("MYSQL_PWD", "MARIADB_ROOT_PASSWORD", mysqlDumpProgram, "-uroot", "--routines", "--triggers", "--databases", "shop"))
			if want := tc.wantDump + " pwd=secret -uroot --routines --triggers --databases shop"; dump != want {
				t.Errorf("dump ran %q, want %q", dump, want)
			}
			client := mysqlClient(job, "root", "MARIADB_ROOT_PASSWORD")
			version := run(client("SELECT VERSION()"))
			if want := tc.wantCli + " pwd=secret -uroot -N -e SELECT VERSION()"; version != want {
				t.Errorf("client ran %q, want %q", version, want)
			}
		})
	}
}
