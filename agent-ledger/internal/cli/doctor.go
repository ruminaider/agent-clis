package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
)

func newDoctorCommand(streams IOStreams, root *rootFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           "doctor",
		Short:         commandShortDescriptions()["doctor"],
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if root != nil && root.JSON {
				asJSON = true
			}
			return runDoctor(streams, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render report as JSON")
	return cmd
}

// doctorReport is the privacy-safe diagnostic payload. No env dumps, no
// secrets, no full file paths beyond what the user already supplied.
type doctorReport struct {
	Status    string        `json:"status"`
	Project   doctorProject `json:"project"`
	LedgerDir doctorLedger  `json:"ledger_dir"`
	Git       doctorGit     `json:"git"`
	Pointer   doctorPointer `json:"pointer"`
	Policy    doctorPolicy  `json:"policy"`
	Checks    []doctorCheck `json:"checks"`
}

type doctorProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Fingerprint string `json:"fingerprint"`
	Root        string `json:"root"`
}

type doctorLedger struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Exists bool   `json:"exists"`
}

type doctorGit struct {
	IsRepo    bool   `json:"is_repo"`
	CommonDir string `json:"common_dir,omitempty"`
	OriginURL string `json:"origin_url,omitempty"`
}

type doctorPointer struct {
	Present bool `json:"present"`
}

type doctorPolicy struct {
	Present bool `json:"present"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func runDoctor(streams IOStreams, asJSON bool) error {
	res, err := project.Resolve(project.Options{
		EnvLedgerDir: os.Getenv(project.EnvVar),
		XDGStateHome: os.Getenv("XDG_STATE_HOME"),
	})
	if err != nil {
		return NewError(ExitGeneric, "resolve_failed", err.Error())
	}

	report := doctorReport{
		Status: "ok",
		Project: doctorProject{
			ID:          res.Identity.ProjectID,
			Slug:        res.Identity.Slug,
			Fingerprint: res.Identity.Fingerprint,
			Root:        res.Root,
		},
		LedgerDir: doctorLedger{
			Path:   res.LedgerDir,
			Source: string(res.LedgerDirSource),
			Exists: dirExists(res.LedgerDir),
		},
		Git: doctorGit{
			IsRepo:    res.GitInfo.IsRepo,
			CommonDir: res.GitInfo.CommonDir,
			OriginURL: res.GitInfo.OriginURL,
		},
		Pointer: doctorPointer{Present: res.Pointer != nil},
		Policy:  doctorPolicy{Present: res.Policy != nil},
		Checks:  buildChecks(res),
	}

	if asJSON {
		enc := json.NewEncoder(streams.Out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return writeDoctorText(streams, report)
}

func buildChecks(res project.Resolution) []doctorCheck {
	checks := []doctorCheck{
		{Name: "project_identity", Status: statusOK("project_identity", res.Identity.Fingerprint != "")},
		{Name: "ledger_dir_resolved", Status: statusOK("ledger_dir_resolved", res.LedgerDir != "")},
	}
	if res.LedgerDir != "" {
		exists := dirExists(res.LedgerDir)
		c := doctorCheck{Name: "ledger_dir_exists", Status: "ok"}
		if !exists {
			c.Status = "warn"
			c.Detail = "directory does not exist; run `agent-ledger init`"
		}
		checks = append(checks, c)
	}
	return checks
}

func statusOK(_ string, ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func writeDoctorText(streams IOStreams, r doctorReport) error {
	w := streams.Out
	fmt.Fprintln(w, "agent-ledger doctor")
	fmt.Fprintf(w, "  project_id:  %s\n", r.Project.ID)
	fmt.Fprintf(w, "  slug:        %s\n", r.Project.Slug)
	fmt.Fprintf(w, "  fingerprint: %s\n", r.Project.Fingerprint)
	fmt.Fprintf(w, "  root:        %s\n", r.Project.Root)
	fmt.Fprintf(w, "  ledger_dir:  %s (%s) exists=%t\n", r.LedgerDir.Path, r.LedgerDir.Source, r.LedgerDir.Exists)
	fmt.Fprintf(w, "  git.is_repo: %t\n", r.Git.IsRepo)
	if r.Git.CommonDir != "" {
		fmt.Fprintf(w, "  git.common_dir: %s\n", r.Git.CommonDir)
	}
	if r.Git.OriginURL != "" {
		fmt.Fprintf(w, "  git.origin: %s\n", r.Git.OriginURL)
	}
	fmt.Fprintf(w, "  pointer:     %t\n", r.Pointer.Present)
	fmt.Fprintf(w, "  policy:      %t\n", r.Policy.Present)
	for _, c := range r.Checks {
		fmt.Fprintf(w, "  - %s: %s %s\n", c.Name, c.Status, c.Detail)
	}
	return nil
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
