package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/doctor"
)

type doctorOpts struct {
	env    envOpener
	asJSON bool
}

// NewDoctorCommand implements SPEC §18.15.
//
// Exit code policy is grounded in the constants currently defined in
// internal/cli/exitcodes.go. SPEC §19.1 specifies a different mapping
// for "configuration error"; aligning the constants is tracked
// separately. For doctor:
//
//   - any check at status=error → ExitStorageIO when the failure is
//     storage-rooted, ExitUsage otherwise (best proxy for
//     "configuration error" given the current constant set).
//   - any check at status=warn → exit 0 (warnings are informational).
func NewDoctorCommand(streams Streams) *cobra.Command {
	o := &doctorOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "doctor",
		Short:         "Run environment and storage diagnostics",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runDoctor(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "Render report as JSON")
	return cmd
}

func runDoctor(streams Streams, o *doctorOpts) error {
	rep := doctor.Run(context.Background(), doctor.Options{
		LedgerDirFlag: o.env.ledgerDirFlag,
		ProjectIDFlag: o.env.projectIDFlag,
	})

	if o.asJSON {
		_ = printJSON(streams.Out, rep)
	} else {
		writeDoctorText(streams, rep)
	}

	if rep.Overall == doctor.StatusError {
		code, machine := classifyDoctorFailure(rep)
		return cli.NewError(code, machine, "doctor reported errors")
	}
	return nil
}

// classifyDoctorFailure picks the most specific exit code for a failed
// doctor run. Storage failures map to ExitStorageIO; everything else
// maps to ExitUsage (the closest analogue to "configuration error" in
// the current constant set; see SPEC §19.1 deviation note above).
func classifyDoctorFailure(rep doctor.Report) (int, string) {
	for _, c := range rep.Checks {
		if c.Status != doctor.StatusError {
			continue
		}
		switch c.Name {
		case "storage", "sqlite_pragmas", "migrations":
			return cli.ExitStorageIO, "storage_unhealthy"
		case "ledger_dir":
			// Treat write-failure as storage; resolution-failure as config.
			if c.Message == "ledger directory is not writable" {
				return cli.ExitStorageIO, "ledger_dir_unwritable"
			}
			return cli.ExitUsage, "ledger_dir_unresolved"
		case "policy", "pointer", "project_identity", "project_resolve":
			return cli.ExitUsage, "configuration_error"
		}
	}
	return cli.ExitUsage, "doctor_failed"
}

func writeDoctorText(streams Streams, rep doctor.Report) {
	fmt.Fprintf(streams.Out, "agent-ledger doctor (overall=%s)\n", rep.Overall)
	for _, c := range rep.Checks {
		fmt.Fprintf(streams.Out, "  [%s] %s", c.Status, c.Name)
		if c.Message != "" {
			fmt.Fprintf(streams.Out, ": %s", c.Message)
		}
		fmt.Fprintln(streams.Out)
	}
}
