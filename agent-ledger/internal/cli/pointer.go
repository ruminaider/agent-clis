// pointer.go implements the read-only `agent-ledger pointer show` command.
// Adapters and operators query it to discover the local pointer file's
// declared values without parsing TOML themselves. The command is a thin
// projection over config.LoadPointer plus the resolved root directory; it
// never writes files and never resolves the ledger directory.
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
)

func newPointerCommand(streams IOStreams, root *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "pointer",
		Short:         "Inspect the local .agent-ledger.toml pointer",
		Long:          "Read-only access to the local pointer file. Subcommands print parsed values for adapters and humans.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newPointerShowCommand(streams, root))
	return cmd
}

// pointerShowReport is the JSON shape printed by `pointer show --json`.
// Present is the authoritative signal for whether the pointer file
// existed; downstream consumers must check it rather than infer
// existence from whether other fields are populated. The optional
// fields use `omitempty` purely to keep the JSON compact, since an
// unset and an empty string both render as the field being absent.
type pointerShowReport struct {
	Present       bool   `json:"present"`
	Path          string `json:"path"`
	Version       int    `json:"version,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	LedgerDir     string `json:"ledger_dir,omitempty"`
	PolicyFile    string `json:"policy_file,omitempty"`
	DefaultTaskID string `json:"default_task_id,omitempty"`
}

func newPointerShowCommand(streams IOStreams, root *rootFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           "show",
		Short:         "Print the parsed local pointer file",
		Long:          "Reads .agent-ledger.toml at the current working directory and prints its parsed contents. Exits 0 when the file is absent (with present=false). Exits non-zero only when the file exists but cannot be parsed.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if root != nil && root.JSON {
				asJSON = true
			}
			return runPointerShow(streams, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runPointerShow(streams IOStreams, asJSON bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return NewError(ExitGeneric, "cwd_failed", err.Error())
	}
	ptr, err := config.LoadPointer(cwd)
	if err != nil {
		return NewError(ExitGeneric, "pointer_parse_failed", err.Error())
	}

	report := pointerShowReport{
		Present: ptr != nil,
		Path:    pointerPath(cwd),
	}
	if ptr != nil {
		report.Version = ptr.Version
		report.ProjectID = ptr.ProjectID
		report.LedgerDir = ptr.LedgerDir
		report.PolicyFile = ptr.PolicyFile
		report.DefaultTaskID = ptr.DefaultTaskID
	}

	if asJSON {
		enc := json.NewEncoder(streams.Out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return writePointerText(streams, report)
}

func writePointerText(streams IOStreams, r pointerShowReport) error {
	w := streams.Out
	fmt.Fprintln(w, "agent-ledger pointer")
	fmt.Fprintf(w, "  path:            %s\n", r.Path)
	fmt.Fprintf(w, "  present:         %t\n", r.Present)
	if !r.Present {
		return nil
	}
	fmt.Fprintf(w, "  version:         %d\n", r.Version)
	if r.ProjectID != "" {
		fmt.Fprintf(w, "  project_id:      %s\n", r.ProjectID)
	}
	if r.LedgerDir != "" {
		fmt.Fprintf(w, "  ledger_dir:      %s\n", r.LedgerDir)
	}
	if r.PolicyFile != "" {
		fmt.Fprintf(w, "  policy_file:     %s\n", r.PolicyFile)
	}
	if r.DefaultTaskID != "" {
		fmt.Fprintf(w, "  default_task_id: %s\n", r.DefaultTaskID)
	}
	return nil
}

func pointerPath(root string) string {
	return root + string(os.PathSeparator) + config.PointerFileName
}
