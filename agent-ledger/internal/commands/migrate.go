package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/migrations"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

type migrateOpts struct {
	env    envOpener
	status bool
	asJSON bool
}

// NewMigrateCommand implements SPEC §18.14.
//
// The default behavior is to apply all pending migrations and print a
// short status line. With --status, the command performs no writes:
// it reports the current schema_version, applied list, and any pending
// migrations so operators can confirm a fresh deploy without forcing
// the schema forward.
func NewMigrateCommand(streams Streams) *cobra.Command {
	o := &migrateOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Apply schema migrations",
		Long:          "Apply schema migrations to the agent-ledger SQLite database. Idempotent: re-running with no pending migrations is a no-op.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runMigrate(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.BoolVar(&o.status, "status", false, "Report applied and pending migrations without applying any")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runMigrate(streams Streams, o *migrateOpts) error {
	res, err := project.Resolve(project.Options{
		LedgerDirFlag: o.env.ledgerDirFlag,
		ProjectIDFlag: o.env.projectIDFlag,
		EnvLedgerDir:  os.Getenv(project.EnvVar),
		XDGStateHome:  os.Getenv("XDG_STATE_HOME"),
	})
	if err != nil {
		return cli.NewError(cli.ExitGeneric, "resolve_failed", err.Error())
	}
	if err := res.Validate(); err != nil {
		return cli.NewError(cli.ExitUsage, "ledger_dir_unset", err.Error())
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, res.LedgerDir)
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "store_open_failed", err.Error())
	}
	defer store.Close()

	if !o.status {
		if err := store.Migrator().Up(ctx); err != nil {
			return cli.NewError(cli.ExitStorageIO, "migrate_failed", err.Error())
		}
	}

	h := store.Health(ctx)
	if !h.PingOK {
		return cli.NewError(cli.ExitStorageIO, "ping_failed", h.PingErr)
	}

	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"ledger_dir":     res.LedgerDir,
			"schema_version": h.SchemaVersion,
			"applied":        h.Applied,
			"pending":        pendingForJSON(h.Pending),
			"status_only":    o.status,
		})
	}

	fmt.Fprintf(streams.Out, "schema_version=%d ledger_dir=%s\n",
		h.SchemaVersion, res.LedgerDir)
	if len(h.Applied) > 0 {
		fmt.Fprintf(streams.Out, "applied (%d):\n", len(h.Applied))
		for _, a := range h.Applied {
			fmt.Fprintf(streams.Out, "  %04d %s applied_at=%s\n", a.Version, a.Name, a.AppliedAt)
		}
	}
	if len(h.Pending) > 0 {
		fmt.Fprintf(streams.Out, "pending (%d):\n", len(h.Pending))
		for _, m := range h.Pending {
			fmt.Fprintf(streams.Out, "  %04d %s\n", m.Version, m.Name)
		}
	}
	return nil
}

// pendingForJSON projects pending migrations into a stable JSON shape
// that does not leak embedded SQL bodies.
func pendingForJSON(in []migrations.Migration) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, m := range in {
		out = append(out, map[string]any{
			"version": m.Version,
			"name":    m.Name,
		})
	}
	return out
}
