package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

type scanOpts struct {
	env    envOpener
	asJSON bool
}

// NewScanCommand walks every JSON-bearing table in the ledger,
// attempts to decode each row's metadata / paths / payload columns,
// and returns one aggregate report. Companion to the routine
// `doctor` health check: doctor reports environment posture; scan
// reports ledger-data integrity.
//
// Exit codes:
//   - 0 if the scan completes and finds zero corrupt rows
//   - 3 (ExitStorageIO) if the scan completes but finds at least one
//     corrupt row OR if a query against the ledger itself fails
//
// The scan does not abort on the first corrupt row. Every row that
// fails to decode is reported, so a single invocation surfaces the
// full corruption picture instead of forcing the operator to fix
// rows one at a time.
func NewScanCommand(streams Streams) *cobra.Command {
	o := &scanOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the ledger for corrupt JSON columns and report all issues",
		Long: `Scan walks every JSON-bearing column in the ledger and reports any
row whose metadata_json, paths_json, or payload_json fails to
decode. The scan does not abort on the first failure: it aggregates
issues across agents, assignments, intents, changes, validations,
conflicts, and events tables and reports them in one report.

Exit code 0 if the scan completes with zero issues. Exit code 3
(ExitStorageIO) if any row is corrupt or if the underlying query
fails.

Use --json for the structured agent-ledger.scan.v1 schema.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runScan(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runScan(streams Streams, o *scanOpts) error {
	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	report, err := d.IntegrityScan(ctx)
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "scan_failed", err.Error())
	}

	if o.asJSON {
		out := map[string]any{
			"schema":      "agent-ledger.scan.v1",
			"tables":      report.Tables,
			"row_total":   report.Total(),
			"issue_count": len(report.Issues),
			"issues":      report.Issues,
		}
		if err := printJSON(streams.Out, out); err != nil {
			return err
		}
	} else {
		writeScanText(streams, report)
	}

	if report.HasIssues() {
		// No further error envelope: the human-readable report or the
		// JSON document already carries the issue list. The exit code
		// signals failure to the caller.
		return cli.NewError(cli.ExitStorageIO, "ledger_corrupt",
			fmt.Sprintf("%d corrupt row(s) found across %d row(s) scanned", len(report.Issues), report.Total())).
			WithDetails(map[string]any{"issue_count": len(report.Issues), "row_total": report.Total()})
	}
	return nil
}

func writeScanText(streams Streams, r domain.IntegrityReport) {
	fmt.Fprintf(streams.Out, "agent-ledger scan: %d rows examined across %d tables\n", r.Total(), len(r.Tables))
	for table, count := range r.Tables {
		fmt.Fprintf(streams.Out, "  %s: %d rows\n", table, count)
	}
	if !r.HasIssues() {
		fmt.Fprintln(streams.Out, "ok: no corrupt rows found")
		return
	}
	fmt.Fprintf(streams.Out, "\n%d corrupt row(s) found:\n", len(r.Issues))
	for _, issue := range r.Issues {
		fmt.Fprintf(streams.Out, "  [%s] %s.%s row=%s: %s\n",
			issue.Kind, issue.Table, issue.Column, issue.RowID, issue.Message)
	}
}
