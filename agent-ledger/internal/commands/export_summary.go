package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/summary"
)

type exportSummaryOpts struct {
	env    envOpener
	task   string
	output string
	asJSON bool
}

// NewExportSummaryCommand implements SPEC §18.12 / §22.
func NewExportSummaryCommand(streams Streams) *cobra.Command {
	o := &exportSummaryOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "export-summary",
		Short:         "Export a privacy-safe task summary for CI",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runExportSummary(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Task ID (required)")
	f.StringVar(&o.output, "output", "", "Path to write the summary JSON to (required)")
	f.BoolVar(&o.asJSON, "json", false, "Echo the summary on stdout as JSON in addition to writing it")
	return cmd
}

func runExportSummary(streams Streams, o *exportSummaryOpts) error {
	if strings.TrimSpace(o.task) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--task is required")
	}
	if strings.TrimSpace(o.output) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--output is required")
	}
	ctx := ctxFor(streams)
	store, res, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	now := store.Clock()()
	doc, err := summary.Build(ctx, summary.Inputs{
		Store:       d,
		Identity:    res.Identity,
		TaskID:      o.task,
		GeneratedAt: now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return mapStorageReadError(err, "summary_build_failed")
	}
	raw, err := summary.Marshal(doc)
	if err != nil {
		return cli.NewError(cli.ExitGeneric, "summary_marshal_failed", err.Error())
	}
	out := o.output
	if !filepath.IsAbs(out) {
		out = filepath.Join(res.Root, out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return cli.NewError(cli.ExitStorageIO, "summary_mkdir_failed", err.Error())
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return cli.NewError(cli.ExitStorageIO, "summary_write_failed", err.Error())
	}
	if o.asJSON {
		// Echo the structure so callers in CI can pipe.
		fmt.Fprint(streams.Out, string(raw))
		return nil
	}
	fmt.Fprintf(streams.Out, "wrote %s task=%s changes=%d validations=%d closed=%v\n",
		out, doc.Task.ID, len(doc.Changes), len(doc.Validations), doc.Closed)
	return nil
}

// sha256HexShort returns the lowercase hex sha256 of s. Used by adopt
// for the reason hash tag (not exported because it is a tiny helper).
func sha256HexShort(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
