package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// quotedCommandPattern finds a command quoted in help text: "ankra ..." up
// to the closing quote.
var quotedCommandPattern = regexp.MustCompile(`"(ankra [^"]+)"`)

// commandWordPattern is a word that names a command, not an argument
// placeholder such as <addon> or a flag.
var commandWordPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// The deprovision and provision help sent people to "ankra hetzner cluster
// stop" and "ankra hetzner cluster start", which do not exist; the commands
// are "ankra cluster hetzner stop|start" (ankra-v3qdto.6). Every command the
// two help texts quote must be one the CLI has.
func TestProvisionAndDeprovisionHelpNameRealCommands(t *testing.T) {
	for _, command := range []struct {
		name string
		long string
	}{
		{name: "cluster deprovision", long: clusterDeprovisionCmd.Long},
		{name: "cluster provision", long: clusterProvisionCmd.Long},
	} {
		matches := quotedCommandPattern.FindAllStringSubmatch(command.long, -1)
		if len(matches) == 0 {
			t.Fatalf("%s help quotes no command; the test no longer checks anything", command.name)
		}
		for _, match := range matches {
			var words []string
			for _, word := range strings.Fields(match[1])[1:] {
				if !commandWordPattern.MatchString(word) {
					break
				}
				words = append(words, word)
			}
			found, remaining, findError := rootCmd.Find(words)
			foundPath := ""
			if found != nil {
				foundPath = found.CommandPath()
			}
			if findError != nil || len(remaining) != 0 || foundPath != "ankra "+strings.Join(words, " ") {
				t.Errorf("%s help quotes %q, which is not a command (found %q, left %v, error %v)",
					command.name, match[1], foundPath, remaining, findError)
			}
		}
	}
}
