// Package commands implements Wave 2 CLI handlers: identify, assign,
// claim, heartbeat, close, status, and conflicts.
//
// Handlers in this package live alongside the cobra glue but the glue
// is registered from cmd/agent-ledger/main.go so package internal/cli
// remains owned by Wave 1.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// Streams aliases cli.IOStreams for ergonomic use inside this package.
type Streams = cli.IOStreams

// envOpener is the resolved-storage handle used by every command.
type envOpener struct {
	streams       Streams
	ledgerDirFlag string
	projectIDFlag string
}

func (e envOpener) resolve() (project.Resolution, error) {
	return project.Resolve(project.Options{
		LedgerDirFlag: e.ledgerDirFlag,
		ProjectIDFlag: e.projectIDFlag,
		EnvLedgerDir:  os.Getenv(project.EnvVar),
		XDGStateHome:  os.Getenv("XDG_STATE_HOME"),
	})
}

// openStore resolves the project, ensures storage exists, runs
// migrations, and returns the SQLite store.
func (e envOpener) openStore(ctx context.Context) (*sqlite.Store, project.Resolution, error) {
	res, err := e.resolve()
	if err != nil {
		return nil, project.Resolution{}, cli.NewError(cli.ExitGeneric, "resolve_failed", err.Error())
	}
	if err := res.Validate(); err != nil {
		return nil, res, cli.NewError(cli.ExitUsage, "ledger_dir_unset", err.Error())
	}
	store, err := sqlite.Open(ctx, res.LedgerDir)
	if err != nil {
		return nil, res, cli.NewError(cli.ExitStorageIO, "store_open_failed", err.Error())
	}
	if err := store.Migrator().Up(ctx); err != nil {
		_ = store.Close()
		return nil, res, cli.NewError(cli.ExitStorageIO, "migrate_failed", err.Error())
	}
	return store, res, nil
}

// addStoreFlags registers --ledger-dir and --project-id on cmd. Most
// commands take these so tests can override. They are hidden from
// shorter help output.
func addStoreFlags(cmd *cobra.Command, e *envOpener) {
	cmd.Flags().StringVar(&e.ledgerDirFlag, "ledger-dir", "", "Override ledger directory (otherwise resolved per SPEC §8)")
	cmd.Flags().StringVar(&e.projectIDFlag, "project-id", "", "Explicit project identifier")
}

// jsonFlag returns whether the inherited --json flag is set on cmd.
func jsonFlag(cmd *cobra.Command) bool {
	v, err := cmd.Root().PersistentFlags().GetBool("json")
	if err == nil && v {
		return true
	}
	v, err = cmd.Flags().GetBool("json")
	if err == nil && v {
		return true
	}
	return false
}

// printJSON renders v as indented JSON to w.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ctxFor returns the command-scoped context.
func ctxFor(_ Streams) context.Context { return context.Background() }

// expandPaths normalizes user-provided paths against the project root.
// It returns the normalized triples plus the absolute directory used
// as the project root for path normalization.
func expandPaths(root string, args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "" {
			continue
		}
		if filepath.IsAbs(a) {
			out = append(out, a)
		} else {
			out = append(out, filepath.Join(root, a))
		}
	}
	return out, nil
}

// pickAgentID returns the first non-empty value from --agent flag or
// $AGENT_ID. Returns "" when neither is set.
func pickAgentID(flag string) string {
	if flag != "" {
		return flag
	}
	return strings.TrimSpace(os.Getenv("AGENT_ID"))
}

// errf helps build cli.Error with formatted message.
func errf(code int, machine, format string, args ...any) *cli.Error {
	return cli.NewError(code, machine, fmt.Sprintf(format, args...))
}

// mapStorageReadError maps storage read failures returned from
// strict domain readers to a typed CLI error. The default fallback
// uses fallbackCode and preserves the caller's legacy code for plain
// SQL errors. domain.MetadataDecodeError and domain.PathsDecodeError
// surface dedicated storage codes so reviewers can pinpoint corrupted
// rows without grepping the underlying message. Returns nil on success
// so callers can chain `if cliErr := mapStorageReadError(err, ...); cliErr != nil { return cliErr }`.
func mapStorageReadError(err error, fallbackCode string) *cli.Error {
	if err == nil {
		return nil
	}
	var mde *domain.MetadataDecodeError
	if errors.As(err, &mde) {
		return cli.NewError(cli.ExitStorageIO, "metadata_corrupt", err.Error()).
			WithDetails(map[string]any{
				"field":  mde.Field,
				"row_id": mde.RowID,
			})
	}
	var pde *domain.PathsDecodeError
	if errors.As(err, &pde) {
		return cli.NewError(cli.ExitStorageIO, "paths_corrupt", err.Error()).
			WithDetails(map[string]any{
				"field":  pde.Field,
				"row_id": pde.RowID,
			})
	}
	return cli.NewError(cli.ExitStorageIO, fallbackCode, err.Error())
}
