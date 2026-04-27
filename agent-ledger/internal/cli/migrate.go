package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

type migrateOpts struct {
	ledgerDir string
	projectID string
}

func newMigrateCommand(streams IOStreams, _ *rootFlags) *cobra.Command {
	o := &migrateOpts{}
	cmd := &cobra.Command{
		Use:           "migrate",
		Short:         commandShortDescriptions()["migrate"],
		Long:          "Apply schema migrations to the agent-ledger SQLite database.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(streams, *o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.projectID, "project-id", "", "Explicit project identifier")
	f.StringVar(&o.ledgerDir, "ledger-dir", "", "Override ledger directory (otherwise resolved per SPEC §8)")
	return cmd
}

func runMigrate(streams IOStreams, o migrateOpts) error {
	res, err := project.Resolve(project.Options{
		LedgerDirFlag: o.ledgerDir,
		ProjectIDFlag: o.projectID,
		EnvLedgerDir:  os.Getenv(project.EnvVar),
		XDGStateHome:  os.Getenv("XDG_STATE_HOME"),
	})
	if err != nil {
		return NewError(ExitGeneric, "resolve_failed", err.Error())
	}
	if err := res.Validate(); err != nil {
		return NewError(ExitUsage, "ledger_dir_unset", err.Error())
	}
	ctx := contextFromCommand(streams)
	store, err := sqlite.Open(ctx, res.LedgerDir)
	if err != nil {
		return NewError(ExitStorageIO, "store_open_failed", err.Error())
	}
	defer store.Close()
	if err := store.Migrator().Up(ctx); err != nil {
		return NewError(ExitStorageIO, "migrate_failed", err.Error())
	}
	v, err := store.Migrator().SchemaVersion(ctx)
	if err != nil {
		return NewError(ExitStorageIO, "schema_version_failed", err.Error())
	}
	fmt.Fprintf(streams.Out, "schema_version=%d ledger_dir=%s\n", v, res.LedgerDir)
	return nil
}
