package cmd

// ankra security fix-candidates and ankra security images declared
// (ankra-oq7jks.9.10, ankra-oq7jks.9.12): the terminal twins of Ask Ankra's
// find_image_fix_candidates and find_image_declarations, over the same
// platform reads.
//
// Both answers carry three outcomes, never two. A candidate tag is fixed
// only when its verdict is carries_fix: not_scanned is unknown, and a
// registry that could not be listed offers no candidate because nothing was
// listed, not because none exists. A declaration search that could not read
// part of what it was asked about says so, and its list of declarations is
// then not the whole list. The human rendering says each of those in words;
// -o json|yaml prints the platform's answer unchanged.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ankra/internal/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

// securityImageReadTimeout bounds one image read. The fix-candidate read
// spends up to 40 seconds on the registry before it answers, and a
// declaration search reads every stack of the clusters it covers.
const securityImageReadTimeout = 2 * time.Minute

var securityFixCandidatesCmd = &cobra.Command{
	Use:   "fix-candidates <image>",
	Short: "Find the newer tags of an image that carry the fix for a vulnerability",
	Long: `Find which newer tags of an image carry the fix for one vulnerability, read
from the image's registry and from what Ankra has already scanned. This is
the same read Ask Ankra makes when you ask it which tag fixes an image you do
not build yourself, such as an add-on's image or a third-party image in a
manifest.

Name the image as it runs (repository:tag, or repository:tag@sha256:...,
with or without a registry) and the vulnerability with --cve, --finding or
both. --finding supplies the CVE when --cve is left out.

The answer names the smallest step that fixes the issue (the newest tag of
the nearest release line verified to carry the fix) and the newest release,
each pinned as tag@sha256, then lists every newer tag examined with one
verdict:

  carries the fix   what Ankra scanned shows the fixed version
  still affected    what Ankra scanned shows an affected version
  not scanned       nothing Ankra scanned decides it: unknown, never fixed

A registry that could not be read, a tag listing cut at its budget and newer
tags left unexamined are said in the answer. In those cases no candidate
means none was found, not that none exists.

Exit codes: 0 the platform answered (read the verdicts: only "carries the
fix" is a fix), 2 the image or vulnerability was not accepted, 3 the finding
was not found, 7 your role cannot read security findings, 1 any other
failure. Pass -o json (or yaml) for the platform's whole answer.`,
	Example: `  ankra security fix-candidates docker:29.0.2-dind --cve CVE-2026-123158
  ankra security fix-candidates ghcr.io/example/api:1.4.2 --finding 0b7c4d21-5f6a-4b8c-9d0e-1f2a3b4c5d6e
  ankra security fix-candidates moby/buildkit:v0.24.0 --cve CVE-2026-123158 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		image := strings.TrimSpace(args[0])
		if image == "" {
			return withExitCode(exitUsage, errors.New("an image is required"))
		}
		cveID, _ := cmd.Flags().GetString("cve")
		findingID, _ := cmd.Flags().GetString("finding")
		cveID, findingID = strings.ToUpper(strings.TrimSpace(cveID)), strings.TrimSpace(findingID)
		if cveID == "" && findingID == "" {
			return withExitCode(exitUsage, errors.New("name the vulnerability with --cve <CVE-ID>, --finding <finding-id> or both"))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), securityImageReadTimeout)
		defer cancel()
		answer, readError := apiClient.FindSecurityImageFixCandidates(ctx, client.SecurityImageFixCandidatesOptions{
			Image: image, CVEID: cveID, FindingID: findingID,
		})
		if readError != nil {
			return securityImageReadError("finding fix candidates", readError)
		}
		if rendered, renderError := renderStructured(cmd, answer); rendered || renderError != nil {
			return renderError
		}
		renderSecurityFixCandidates(cmd.OutOrStdout(), answer)
		return nil
	},
}

var securityImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "Find where an image is declared and what runs it",
	Long: `Read where an image is used across your clusters: the stacks that declare it
and the workloads that run it. To read an image's bill of materials, use
'ankra security sbom images'.`,
}

var securityImagesDeclaredCmd = &cobra.Command{
	Use:   "declared <image>",
	Short: "Find every stack that declares an image, the file and line to change, and what runs it",
	Long: `Find where an image is declared across the stacks of the clusters you can
read. This is the same read Ask Ankra makes when it tells you where to change
an image.

Each declaration names its cluster, stack and member (an add-on's values or
a manifest), the key path and the stored line, and how the declaration is
changed: through a GitOps pull request, a stack draft or an application. On
a cluster with a GitOps repository it also gives the file, and the line when
Git holds what Ankra stores. Workloads running the image that no stack
declares, and those whose declaration is not known, are listed too.

Name the image as a repository such as moby/buildkit or docker:29.0.2-dind,
with or without a registry, tag or digest. Every cluster you can read is
searched unless you name clusters with --cluster.

When part of the search could not be read (a stack member, a cluster's
GitOps wiring, the running inventory) the answer says so and lists what it
missed. An empty or short list of declarations then does not mean the image
is declared nowhere else.

Exit codes: 0 the platform answered (check completeness: -o json carries
completeness.is_complete), 2 the image was not accepted, 3 a --cluster is not
a cluster you can read, 1 any other failure. Pass -o json (or yaml) for the
platform's whole answer.`,
	Example: `  ankra security images declared docker:29.0.2-dind
  ankra security images declared moby/buildkit --cluster production --cluster staging
  ankra security images declared ghcr.io/example/api -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		image := strings.TrimSpace(args[0])
		if image == "" {
			return withExitCode(exitUsage, errors.New("an image is required"))
		}
		clusterFlags, _ := cmd.Flags().GetStringSlice("cluster")
		clusterIDs := []string{}
		for _, clusterFlag := range clusterFlags {
			if strings.TrimSpace(clusterFlag) == "" {
				continue
			}
			clusterID, resolveError := resolveSecurityClusterID(cmd, strings.TrimSpace(clusterFlag))
			if resolveError != nil {
				return resolveError
			}
			clusterIDs = append(clusterIDs, clusterID)
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), securityImageReadTimeout)
		defer cancel()
		answer, readError := apiClient.FindSecurityImageDeclarations(ctx, client.SecurityImageDeclarationsOptions{
			Image: image, ClusterIDs: clusterIDs,
		})
		if readError != nil {
			return securityImageReadError("finding where the image is declared", readError)
		}
		if rendered, renderError := renderStructured(cmd, answer); rendered || renderError != nil {
			return renderError
		}
		renderSecurityImageDeclarations(cmd.OutOrStdout(), answer)
		return nil
	},
}

// securityImageReadError classifies a refused image read: a 400 (an image
// or vulnerability the platform did not accept, a finding that names
// another CVE) and a 422 (a malformed parameter) are fixed by changing the
// invocation, so they are usage errors; every other status keeps the
// shared classification (403 to 7, 404 to 3).
func securityImageReadError(action string, readError error) error {
	wrapped := fmt.Errorf("%s: %w", action, readError)
	var unexpected *client.UnexpectedResponseError
	if errors.As(readError, &unexpected) &&
		(unexpected.StatusCode == http.StatusBadRequest || unexpected.StatusCode == http.StatusUnprocessableEntity) {
		return withExitCode(exitUsage, wrapped)
	}
	return wrapped
}

// fixCandidateVerdictText says a verdict in words. An unrecognised verdict
// is printed as the platform sent it, never as a fix.
func fixCandidateVerdictText(verdict string) string {
	switch verdict {
	case client.FixCandidateVerdictCarriesFix:
		return "carries the fix"
	case client.FixCandidateVerdictStillAffected:
		return "still affected"
	case client.FixCandidateVerdictNotScanned:
		return "not scanned (unknown, not fixed)"
	default:
		return verdict
	}
}

// fixCandidatePickText names one tag the answer picked, pinned when the
// platform pinned it.
func fixCandidatePickText(pick *client.FixCandidatePick) string {
	name := pick.Pin
	if name == "" {
		name = pick.Tag
	}
	return name + " (" + fixCandidateVerdictText(pick.Verdict) + ")"
}

func renderSecurityFixCandidates(out io.Writer, answer *client.SecurityImageFixCandidates) {
	_, _ = fmt.Fprintf(out, "%s · %s\n", answer.Image, answer.CVEID)
	running := answer.Running
	runningName := running.Tag
	if running.Digest != "" {
		runningName = strings.TrimSpace(runningName + " " + running.Digest)
	}
	if runningName != "" {
		_, _ = fmt.Fprintf(out, "Running: %s\n", runningName)
	}
	if len(running.Packages) > 0 {
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Package", "Type", "Installed", "Fixed in", "Occurrences"})
		for _, affected := range running.Packages {
			writer.AppendRow(table.Row{affected.Package, affected.PackageType, affected.InstalledVersion,
				affected.FixedVersion, affected.Occurrences})
		}
		writer.Render()
	}
	if running.Detail != "" {
		_, _ = fmt.Fprintln(out, running.Detail)
	}

	_, _ = fmt.Fprintln(out)
	if answer.Answer.Summary != "" {
		_, _ = fmt.Fprintln(out, answer.Answer.Summary)
	}
	if pick := answer.Answer.SmallestStepWithFix; pick != nil {
		_, _ = fmt.Fprintf(out, "Smallest step with the fix: %s\n", fixCandidatePickText(pick))
	} else {
		_, _ = fmt.Fprintln(out, "Smallest step with the fix: none verified")
	}
	if pick := answer.Answer.NewestRelease; pick != nil {
		_, _ = fmt.Fprintf(out, "Newest release: %s\n", fixCandidatePickText(pick))
	}

	if len(answer.Candidates) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Candidates (%d)\n", len(answer.Candidates))
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Tag", "Line", "Verdict", "Pin"})
		for _, candidate := range answer.Candidates {
			writer.AppendRow(table.Row{candidate.Tag, candidate.Line, fixCandidateVerdictText(candidate.Verdict), candidate.Pin})
		}
		writer.Render()
		for _, candidate := range answer.Candidates {
			for _, evidence := range candidate.Evidence {
				line := fmt.Sprintf("  %s: %s %s, %s", candidate.Tag, evidence.Package,
					fixCandidateEvidenceVersion(evidence), fixCandidateVerdictText(evidence.Verdict))
				if evidence.Source != "" && evidence.Source != "none" {
					line += " (from " + strings.ReplaceAll(evidence.Source, "_", " ") + ")"
				}
				if evidence.Detail != "" {
					line += ": " + evidence.Detail
				}
				_, _ = fmt.Fprintln(out, line)
			}
			if candidate.RegistryDetail != "" {
				_, _ = fmt.Fprintf(out, "  %s: %s\n", candidate.Tag, candidate.RegistryDetail)
			}
		}
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, fixCandidateRegistryText(answer.Registry))
	if len(answer.UnexaminedTags) > 0 {
		_, _ = fmt.Fprintf(out, "Newer tags not examined (%d), which may carry the fix: %s\n",
			len(answer.UnexaminedTags), strings.Join(answer.UnexaminedTags, ", "))
	}
	if answer.HowToRead != "" {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, answer.HowToRead)
	}
}

// fixCandidateEvidenceVersion is the version a candidate's evidence saw.
func fixCandidateEvidenceVersion(evidence client.FixCandidateEvidence) string {
	if evidence.InstalledVersion == "" {
		return "(version not known)"
	}
	return evidence.InstalledVersion
}

// fixCandidateRegistryText says how the tag listing went. An unreadable or
// unread listing is said as the reason there is no candidate, so an empty
// candidate list never reads as "no newer tag carries the fix".
func fixCandidateRegistryText(registry client.FixCandidateRegistryListing) string {
	where := strings.Trim(registry.Registry+"/"+registry.Repository, "/")
	var text string
	switch registry.State {
	case client.FixCandidateRegistryRead:
		text = fmt.Sprintf("Registry %s: %d tags listed, %d newer, %d manifests read.", where,
			registry.TagsListed, registry.NewerTags, registry.ManifestsRead)
		if !registry.ListingComplete {
			text += " The listing stopped at its page budget, so a newer tag missing from it was not read."
		}
	case client.FixCandidateRegistryAbsent:
		text = fmt.Sprintf("Registry %s: the registry says the repository does not exist.", where)
	case client.FixCandidateRegistryUnreadable, client.FixCandidateRegistryNotRead:
		text = fmt.Sprintf("Registry %s: the tag listing was not read, so no candidate is offered because "+
			"nothing was listed, not because none exists.", where)
	default:
		text = fmt.Sprintf("Registry %s: %s.", where, registry.State)
	}
	if registry.Detail != "" {
		text += " " + registry.Detail
	}
	return text
}

func renderSecurityImageDeclarations(out io.Writer, answer *client.SecurityImageDeclarations) {
	image := answer.Image
	resolved := image.Repository
	switch {
	case image.Digest != "":
		resolved += "@" + image.Digest
	case image.Tag != "":
		resolved += ":" + image.Tag
	}
	if resolved != "" && resolved != image.Input {
		_, _ = fmt.Fprintf(out, "%s (as written: %s)\n", resolved, image.Input)
	} else {
		_, _ = fmt.Fprintln(out, image.Input)
	}
	_, _ = fmt.Fprintf(out, "Searched %d cluster(s). %s\n", answer.ClustersSearched, imageDeclarationsCompletenessText(answer.Completeness))

	_, _ = fmt.Fprintln(out)
	switch {
	case len(answer.Declarations) == 0 && answer.Completeness.IsComplete:
		_, _ = fmt.Fprintln(out, "No stack on the searched clusters declares this image.")
	case len(answer.Declarations) == 0:
		_, _ = fmt.Fprintln(out, "No declaration was found, but the search is incomplete, so this does not mean no stack declares the image.")
	default:
		_, _ = fmt.Fprintf(out, "Declarations (%d)\n", len(answer.Declarations))
		writer := newSecurityTable(out)
		writer.AppendHeader(table.Row{"Cluster", "Stack", "Member", "Where", "Reference", "Change via", "Git"})
		for _, declaration := range answer.Declarations {
			writer.AppendRow(table.Row{declaration.ClusterName, declaration.StackName,
				declaration.MemberKind + " " + declaration.MemberName, imageDeclarationWhereText(declaration),
				declaration.Reference, imageDeclarationChangeViaText(declaration), imageDeclarationGitText(declaration)})
		}
		writer.Render()
		if answer.DeclarationsCapped {
			_, _ = fmt.Fprintln(out, "The list of declarations was cut at its cap: more stacks declare the image than are listed.")
		}
	}

	renderImageRunningWorkloads(out, "Running, declared by no stack", answer.RunningUndeclared)
	renderImageRunningWorkloads(out, "Running, built and deployed by an application", answer.RunningByApplication)
	renderImageRunningWorkloads(out, "Running, declaration not known", answer.RunningMatchUnknown)
	switch answer.RunningInventory {
	case "", "read":
	case "capped":
		_, _ = fmt.Fprintln(out, "\nThe running-container read was cut at its cap: more workloads may run the image than are listed.")
	default:
		_, _ = fmt.Fprintf(out, "\nThe running containers could not be read (%s), so what runs the image is not known.\n",
			answer.RunningInventory)
	}

	if len(answer.UnresolvedMentions) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Mentions that could not be resolved (%d)\n", len(answer.UnresolvedMentions))
		for _, mention := range answer.UnresolvedMentions {
			_, _ = fmt.Fprintf(out, "  %s/%s %s %s line %d %s: %s\n", mention.ClusterName, mention.StackName,
				mention.MemberKind, mention.MemberName, mention.StoredLine, mention.KeyPath, mention.Reason)
		}
		if answer.UnresolvedMentionsCapped {
			_, _ = fmt.Fprintln(out, "  The list was cut at its cap.")
		}
	}
	if len(answer.NotSearched) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Not searched (%d)\n", len(answer.NotSearched))
		for _, member := range answer.NotSearched {
			_, _ = fmt.Fprintf(out, "  %s/%s %s %s: %s\n", member.ClusterName, member.StackName, member.MemberKind,
				member.MemberName, member.Reason)
		}
	}
	if len(answer.ClustersExcluded) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Clusters left out (%d)\n", len(answer.ClustersExcluded))
		for _, excluded := range answer.ClustersExcluded {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", excluded.ClusterName, strings.ReplaceAll(excluded.Reason, "_", " "))
		}
		if answer.ClustersExcludedCapped {
			_, _ = fmt.Fprintln(out, "  The list was cut at its cap.")
		}
	}
}

// imageDeclarationsCompletenessText says whether the search read everything
// and, when it did not, what it missed.
func imageDeclarationsCompletenessText(completeness client.ImageDeclarationsCompleteness) string {
	if completeness.IsComplete {
		return "The search is complete."
	}
	reasons := []string{}
	for _, reason := range completeness.IncompleteReasons {
		text := strings.ReplaceAll(reason.Code, "_", " ")
		if reason.Count > 0 {
			text = fmt.Sprintf("%s (%d)", text, reason.Count)
		}
		reasons = append(reasons, text)
	}
	if len(reasons) == 0 {
		return "The search is incomplete."
	}
	return "The search is incomplete: " + strings.Join(reasons, ", ") + "."
}

// imageDeclarationWhereText is where in the member the image is declared.
func imageDeclarationWhereText(declaration client.ImageDeclaration) string {
	parts := []string{}
	if declaration.ResourceKind != "" || declaration.ResourceName != "" {
		parts = append(parts, strings.TrimSpace(declaration.ResourceKind+" "+declaration.ResourceName))
	}
	if declaration.KeyPath != "" {
		parts = append(parts, declaration.KeyPath)
	}
	if declaration.StoredLine > 0 {
		parts = append(parts, fmt.Sprintf("line %d", declaration.StoredLine))
	}
	return strings.Join(parts, ", ")
}

// imageDeclarationChangeViaText says how the declaration is changed.
func imageDeclarationChangeViaText(declaration client.ImageDeclaration) string {
	text := strings.ReplaceAll(declaration.ChangeVia, "_", " ")
	if declaration.StackHasOpenDraft {
		text += " (the stack has an open draft)"
	}
	return text
}

// imageDeclarationGitText is the Git file and line to change, or why it is
// not known.
func imageDeclarationGitText(declaration client.ImageDeclaration) string {
	gitops := declaration.GitOps
	if gitops == nil {
		if declaration.GitOpsUnknown != "" {
			return "not known: " + declaration.GitOpsUnknown
		}
		return ""
	}
	location := gitops.FilePath
	if gitops.Line != nil {
		location = fmt.Sprintf("%s:%d", location, *gitops.Line)
	} else if gitops.LineUnknownReason != "" {
		location += " (line not known: " + gitops.LineUnknownReason + ")"
	}
	return strings.TrimSpace(gitops.Repository + " " + location)
}

func renderImageRunningWorkloads(out io.Writer, title string, workloads []client.ImageRunningWorkload) {
	if len(workloads) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "%s (%d)\n", title, len(workloads))
	for _, workload := range workloads {
		line := fmt.Sprintf("  %s/%s %s %s, container %s, %d pod(s): %s", workload.ClusterName, workload.Namespace,
			workload.WorkloadKind, workload.WorkloadName, workload.ContainerName, workload.Pods, workload.Image)
		if len(workload.MatchUnknownReasons) > 0 {
			line += " (not known because: " + strings.ReplaceAll(strings.Join(workload.MatchUnknownReasons, ", "), "_", " ") + ")"
		}
		_, _ = fmt.Fprintln(out, line)
	}
}

func init() {
	securityCmd.AddCommand(securityFixCandidatesCmd, securityImagesCmd)
	securityImagesCmd.AddCommand(securityImagesDeclaredCmd)

	securityFixCandidatesCmd.Flags().String("cve", "", "The vulnerability by CVE id")
	securityFixCandidatesCmd.Flags().String("finding", "", "The vulnerability by security finding id; supplies the CVE when --cve is left out")
	securityImagesDeclaredCmd.Flags().StringSlice("cluster", nil, "Search only this cluster (name or id); repeatable. Default: every cluster you can read")

	registerSecurityProjectScopeFlag(securityFixCandidatesCmd, securityImagesDeclaredCmd)
	registerStructuredOutputFlags(securityFixCandidatesCmd, securityImagesDeclaredCmd)
}
