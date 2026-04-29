package commands

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/changes"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

type recordOpts struct {
	env         envOpener
	intent      string
	summary     string
	validations []string
	includeDiff bool
	yes         bool
	agent       string
	asJSON      bool
}

// NewRecordCommand implements SPEC §18.6.
func NewRecordCommand(streams Streams) *cobra.Command {
	o := &recordOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "record <path>...",
		Short:         "Record a change made under an open intent",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runRecord(streams, o, args)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.intent, "intent", "", "Intent ID this change was made under (required)")
	f.StringVar(&o.summary, "summary", "", "Human-readable summary of the change (required)")
	f.StringArrayVar(&o.validations, "validation", nil, "Validation result as <command>:<status> (repeatable)")
	f.BoolVar(&o.includeDiff, "include-diff", false, "Read full diff from stdin and store it as a blob")
	f.BoolVar(&o.yes, "yes", false, "Acknowledge the privacy implications of --include-diff")
	f.StringVar(&o.agent, "agent", "", "Override AGENT_ID env for this record")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runRecord(streams Streams, o *recordOpts, args []string) error {
	if strings.TrimSpace(o.intent) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--intent is required")
	}
	if strings.TrimSpace(o.summary) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--summary is required")
	}
	if err := privacy.AssertSafe("summary", o.summary); err != nil {
		return cli.NewError(cli.ExitValidation, "summary_unsafe", err.Error())
	}
	agentID := pickAgentID(o.agent)
	if agentID == "" {
		return cli.NewError(cli.ExitUsage, "missing_agent", "AGENT_ID not set; pass --agent or run identify first")
	}

	// Pre-parse validations so a malformed entry fails before any DB
	// write, satisfying the "write no event" rule for invalid input.
	type parsedValidation struct {
		cmd, status string
	}
	parsed := make([]parsedValidation, 0, len(o.validations))
	for _, v := range o.validations {
		cmdStr, status, err := changes.ParseValidation(v)
		if err != nil {
			return cli.NewError(cli.ExitValidation, "invalid_validation", err.Error())
		}
		if err := privacy.AssertSafe("validation command", cmdStr); err != nil {
			return cli.NewError(cli.ExitValidation, "validation_unsafe", err.Error())
		}
		parsed = append(parsed, parsedValidation{cmdStr, status})
	}

	ctx := ctxFor(streams)
	store, res, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	intent, err := d.IntentByID(ctx, o.intent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cli.NewError(cli.ExitNotFound, "intent_not_found", fmt.Sprintf("intent %q not found", o.intent))
		}
		return mapStorageReadError(err, "intent_lookup_failed")
	}
	if intent.Status != domain.IntentActive {
		return cli.NewError(cli.ExitConflict, "intent_not_active", fmt.Sprintf("intent %s is %s, expected active", intent.IntentID, intent.Status))
	}

	// Build the claimed-path set.
	claimed, err := d.IntentPathHashes(ctx, intent.IntentID)
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "intent_paths_failed", err.Error())
	}

	// Normalize and check every supplied path against the intent.
	abspaths, err := expandPaths(res.Root, args)
	if err != nil {
		return cli.NewError(cli.ExitGeneric, "path_expand_failed", err.Error())
	}
	type cp struct {
		display, real, hash string
	}
	want := make([]cp, 0, len(abspaths))
	for _, p := range abspaths {
		n, err := paths.Normalize(res.Root, p)
		if err != nil {
			if paths.IsOutsideProject(err) {
				return cli.NewError(cli.ExitGeneric, "path_outside_project", err.Error()).
					WithDetails(map[string]any{"path": p, "finding": "PATH_OUTSIDE_ASSIGNMENT"})
			}
			return cli.NewError(cli.ExitGeneric, "path_normalize_failed", err.Error())
		}
		if _, ok := claimed[n.PathHash]; !ok {
			// SPEC §18.6: write no event, exit 1.
			return cli.NewError(cli.ExitGeneric, "unclaimed_path",
				fmt.Sprintf("path %q is not in intent %s claimed paths; claim it first or use 'agent-ledger adopt'", n.Display, intent.IntentID)).
				WithDetails(map[string]any{
					"path":      n.Display,
					"intent_id": intent.IntentID,
					"finding":   "UNCLAIMED_CHANGE",
				})
		}
		want = append(want, cp{display: n.Display, real: n.RealPath, hash: n.PathHash})
	}

	// Optional diff capture.
	patchHash := ""
	patchRef := ""
	if o.includeDiff {
		if !o.yes && !isInteractive(streams) {
			return cli.NewError(cli.ExitValidation, "include_diff_requires_yes",
				"--include-diff stores raw patch text; pass --yes to acknowledge in non-interactive mode")
		}
		raw, err := readAll(streams.In)
		if err != nil {
			return cli.NewError(cli.ExitGeneric, "diff_read_failed", err.Error())
		}
		if len(raw) == 0 {
			return cli.NewError(cli.ExitValidation, "include_diff_empty",
				"--include-diff was set but stdin contained no diff content")
		}
		norm := changes.NormalizeDiff(raw)
		patchHash = changes.HashDiff(raw)
		blobsRoot := storage.Layout{Dir: store.LedgerDir()}.BlobsDir()
		ref, err := changes.WriteBlob(blobsRoot, norm)
		if err != nil {
			return cli.NewError(cli.ExitStorageIO, "blob_write_failed", err.Error())
		}
		patchRef = "blobs/sha256/" + ref
	}

	// Build change_paths rows.
	chPaths := make([]domain.ChangePath, 0, len(want))
	for _, w := range want {
		chPaths = append(chPaths, domain.ChangePath{
			Path:     w.display,
			RealPath: w.real,
			PathHash: w.hash,
			PatchSHA: patchHash,
			Status:   domain.PathStatusModified,
		})
	}

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: agentID, AgentKind: "worker"}); err != nil {
		return cli.NewError(cli.ExitStorageIO, "agent_upsert_failed", err.Error())
	}

	change, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change: domain.Change{
			IntentID:     intent.IntentID,
			AssignmentID: intent.AssignmentID,
			TaskID:       intent.TaskID,
			AgentID:      agentID,
			ActorKind:    domain.ActorAgent,
			Summary:      o.summary,
		},
		Paths:       chPaths,
		EventType:   "change.recorded",
		PatchRef:    patchRef,
		PatchSHA256: patchHash,
	})
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "change_insert_failed", err.Error())
	}

	validationIDs := make([]string, 0, len(parsed))
	for _, v := range parsed {
		written, err := d.InsertValidation(ctx, domain.Validation{
			ChangeID: change.ChangeID,
			TaskID:   intent.TaskID,
			Command:  v.cmd,
			Status:   v.status,
		})
		if err != nil {
			return cli.NewError(cli.ExitStorageIO, "validation_insert_failed", err.Error())
		}
		validationIDs = append(validationIDs, written.ValidationID)
	}

	if o.asJSON {
		out := map[string]any{
			"change_id":    change.ChangeID,
			"event_id":     change.EventID,
			"intent_id":    intent.IntentID,
			"task_id":      intent.TaskID,
			"agent_id":     agentID,
			"paths":        displayChangePaths(chPaths),
			"validations":  validationIDs,
			"patch_sha256": patchHash,
		}
		if patchRef != "" {
			out["patch_ref"] = patchRef
		}
		return printJSON(streams.Out, out)
	}
	fmt.Fprintf(streams.Out, "change_id=%s intent=%s paths=%d validations=%d\n",
		change.ChangeID, intent.IntentID, len(chPaths), len(validationIDs))
	return nil
}

func displayChangePaths(p []domain.ChangePath) []map[string]any {
	out := make([]map[string]any, 0, len(p))
	for _, x := range p {
		out = append(out, map[string]any{
			"path":      x.Path,
			"path_hash": x.PathHash,
			"status":    x.Status,
		})
	}
	return out
}

// readAll drains r safely. Wrapper to keep the import list short.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// isInteractive reports whether the input stream looks like a TTY. We
// only treat actual *os.File handles bound to a terminal as
// interactive: bytes.Buffer, pipes, and redirected stdin are
// non-interactive by design so subprocess tests reliably hit the
// non-interactive path.
func isInteractive(streams Streams) bool {
	f, ok := streams.In.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}
