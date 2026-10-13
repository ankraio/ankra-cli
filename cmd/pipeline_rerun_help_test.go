package cmd

import (
	"strings"
	"testing"
)

// The rerun help follows the platform's rerun planning (cluster#4496): a
// re-run is planned as the event of the run it repeats, so a re-run of a push
// runs its push-only stages. The old text said a re-run's trigger filters a
// push-only stage out, which stopped being true (ankra-831fzt.1).
func TestPipelineRerunHelpDescribesRerunPlanning(t *testing.T) {
	commands := map[string]string{
		"pipeline rerun":             newPipelineRerunCommand().Long,
		"application pipeline rerun": newApplicationPipelineRerunCommand().Long,
	}
	for name, long := range commands {
		help := strings.Join(strings.Fields(long), " ")
		for _, want := range []string{
			"A re-run of a push run is planned as a push, so its push-only stages run.",
			"is planned as a pull request",
			"does not become the push-to-deploy candidate",
			"the steps that were skipped because of a failure",
			"is refused with the reason",
		} {
			if !strings.Contains(help, want) {
				t.Errorf("%s help is missing %q:\n%s", name, want, long)
			}
		}
		if strings.Contains(help, "filters out") {
			t.Errorf("%s help still says a re-run's trigger filters a stage out:\n%s", name, long)
		}
	}
}
