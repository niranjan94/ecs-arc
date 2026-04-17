// One-shot utility to delete a fixed list of GitHub Actions scale sets via
// the same library and auth path the ecs-arc controller uses. List-only by
// default; pass --apply to actually delete.
//
// Edit the `targets` slice below to match the scale set names you want to
// delete, then run from the repo root:
//
//	go run ./scripts/delete-scalesets \
//	  --org <github-org> \
//	  --client-id <github-app-client-id> \
//	  --installation-id <github-app-installation-id> \
//	  --private-key-file /path/to/app-private-key.pem
//
// List-only by default. Add --apply to delete. Add --skip-managed-check to
// delete scale sets that lack the ecs-arc.managed label.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/actions/scaleset"
)

const managedLabel = "ecs-arc.managed"

var targets = []string{
	"the-outpost-runner-small",
	"the-outpost-runner-medium",
	"the-outpost-runner-large",
	"the-outpost-runner-small-x64",
	"the-outpost-runner-medium-x64",
	"the-outpost-runner-large-x64",
	"the-outpost-runner-small-arm64",
	"the-outpost-runner-medium-arm64",
	"the-outpost-runner-large-arm64",
}

func main() {
	var (
		org            = flag.String("org", "", "GitHub organization")
		clientID       = flag.String("client-id", "", "GitHub App Client ID")
		installationID = flag.Int64("installation-id", 0, "GitHub App Installation ID")
		keyFile        = flag.String("private-key-file", "", "Path to GitHub App PEM private key")
		runnerGroupID  = flag.Int("runner-group-id", 1, "Runner group ID (Default = 1)")
		apply          = flag.Bool("apply", false, "Actually delete (default: list only)")
		skipLabelCheck = flag.Bool("skip-managed-check", false, "Delete even if ecs-arc.managed label is missing")
	)
	flag.Parse()

	if *org == "" || *clientID == "" || *installationID == 0 || *keyFile == "" {
		fmt.Fprintln(os.Stderr, "missing required flag")
		flag.Usage()
		os.Exit(2)
	}

	keyBytes, err := os.ReadFile(*keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read private key: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	client, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: "https://github.com/" + *org,
		GitHubAppAuth: scaleset.GitHubAppAuth{
			ClientID:       *clientID,
			InstallationID: *installationID,
			PrivateKey:     string(keyBytes),
		},
		SystemInfo: scaleset.SystemInfo{
			System:    "ecs-arc",
			Subsystem: "delete-scalesets",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create scaleset client: %v\n", err)
		os.Exit(1)
	}

	all, err := client.ListRunnerScaleSets(ctx, *runnerGroupID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list scale sets: %v\n", err)
		os.Exit(1)
	}

	wanted := make(map[string]bool, len(targets))
	for _, n := range targets {
		wanted[n] = true
	}

	type match struct {
		ss      scaleset.RunnerScaleSet
		managed bool
	}
	var matches []match
	for _, ss := range all {
		if !wanted[ss.Name] {
			continue
		}
		matches = append(matches, match{ss: ss, managed: hasManagedLabel(ss.Labels)})
	}

	fmt.Printf("Listed %d scale sets in runner group %d, %d match the target list.\n\n",
		len(all), *runnerGroupID, len(matches))

	for _, m := range matches {
		marker := "[MANAGED]"
		if !m.managed {
			marker = "[UNMANAGED]"
		}
		fmt.Printf("  %s  id=%-8d  name=%s\n            labels=%s\n",
			marker, m.ss.ID, m.ss.Name, labelString(m.ss.Labels))
	}

	found := make(map[string]bool, len(matches))
	for _, m := range matches {
		found[m.ss.Name] = true
	}
	var missing []string
	for _, n := range targets {
		if !found[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		fmt.Printf("\nNot found in runner group %d: %v\n", *runnerGroupID, missing)
	}

	if !*apply {
		fmt.Println("\nDry run. Re-run with --apply to delete the [MANAGED] entries above.")
		return
	}

	fmt.Println("\nApplying deletions...")
	var deleted, skipped, failed int
	for _, m := range matches {
		if !m.managed && !*skipLabelCheck {
			fmt.Printf("  SKIP  id=%d name=%s (no %s label; pass --skip-managed-check to override)\n",
				m.ss.ID, m.ss.Name, managedLabel)
			skipped++
			continue
		}
		if err := client.DeleteRunnerScaleSet(ctx, m.ss.ID); err != nil {
			fmt.Printf("  FAIL  id=%d name=%s: %v\n", m.ss.ID, m.ss.Name, err)
			failed++
			continue
		}
		fmt.Printf("  OK    id=%d name=%s\n", m.ss.ID, m.ss.Name)
		deleted++
	}
	fmt.Printf("\nDone. deleted=%d skipped=%d failed=%d\n", deleted, skipped, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func hasManagedLabel(labels []scaleset.Label) bool {
	for _, l := range labels {
		if l.Name == managedLabel {
			return true
		}
	}
	return false
}

func labelString(labels []scaleset.Label) string {
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		parts = append(parts, fmt.Sprintf("%s(%s)", l.Name, l.Type))
	}
	return strings.Join(parts, ",")
}
