package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"database/sql"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

type statusOpts struct {
	env    envOpener
	intent string
	task   string
	path   string
	asJSON bool
}

// NewStatusCommand implements SPEC §18.8.
func NewStatusCommand(streams Streams) *cobra.Command {
	o := &statusOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show active claims, recent changes, and conflicts",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runStatus(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.intent, "intent", "", "Show only the named intent")
	f.StringVar(&o.task, "task", "", "Filter by task ID")
	f.StringVar(&o.path, "path", "", "Filter by path")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runStatus(streams Streams, o *statusOpts) error {
	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	if o.intent != "" {
		in, err := d.IntentByID(ctx, o.intent)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return cli.NewError(cli.ExitNotFound, "intent_not_found", "intent not found")
			}
			return cli.NewError(cli.ExitStorageIO, "intent_lookup_failed", err.Error())
		}
		ipaths, _ := d.IntentPaths(ctx, in.IntentID)
		if o.asJSON {
			return printJSON(streams.Out, intentJSON(in, ipaths))
		}
		writeIntentText(streams.Out.(interface {
			Write(p []byte) (n int, err error)
		}), in, ipaths)
		return nil
	}

	intents, err := d.ListActiveIntents(ctx, o.task)
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "intents_list_failed", err.Error())
	}
	confs, err := d.ListConflicts(ctx, o.task, "detected")
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "conflicts_list_failed", err.Error())
	}
	stale := staleIntents(intents, store.Clock()())

	if o.asJSON {
		ints := make([]map[string]any, 0, len(intents))
		for _, in := range intents {
			ipaths, _ := d.IntentPaths(ctx, in.IntentID)
			ints = append(ints, intentJSON(in, ipaths))
		}
		cs := make([]map[string]any, 0, len(confs))
		for _, c := range confs {
			cs = append(cs, conflictJSON(c))
		}
		return printJSON(streams.Out, map[string]any{
			"active_intents": ints,
			"conflicts":      cs,
			"stale_intents":  staleIDs(stale),
			"recent_changes": recentChanges(ctx, store, o.task),
		})
	}
	w := streams.Out
	fmt.Fprintf(w, "Active intents (%d):\n", len(intents))
	for _, in := range intents {
		ipaths, _ := d.IntentPaths(ctx, in.IntentID)
		fmt.Fprintf(w, "  %s task=%s agent=%s policy=%s paths=%d\n", in.IntentID, in.TaskID, in.AgentID, in.ConflictPolicy, len(ipaths))
	}
	fmt.Fprintf(w, "Conflicts (%d):\n", len(confs))
	for _, c := range confs {
		fmt.Fprintf(w, "  %s policy=%s path=%s status=%s\n", c.ConflictID, c.Policy, c.Path, c.Status)
	}
	if len(stale) > 0 {
		fmt.Fprintf(w, "Stale intents (%d):\n", len(stale))
		for _, in := range stale {
			fmt.Fprintf(w, "  %s expires=%s\n", in.IntentID, in.HeartbeatExpiresAt)
		}
	}
	return nil
}

func intentJSON(in domain.Intent, ipaths []domain.IntentPath) map[string]any {
	pl := make([]map[string]any, 0, len(ipaths))
	for _, p := range ipaths {
		pl = append(pl, map[string]any{
			"path":      p.Path,
			"path_hash": p.PathHash,
			"access":    p.AccessMode,
		})
	}
	out := map[string]any{
		"intent_id":       in.IntentID,
		"task_id":         in.TaskID,
		"agent_id":        in.AgentID,
		"assignment_id":   in.AssignmentID,
		"access_mode":     in.AccessMode,
		"conflict_policy": in.ConflictPolicy,
		"status":          in.Status,
		"opened_at":       in.OpenedAt,
		"paths":           pl,
	}
	if in.LastHeartbeatAt != "" {
		out["last_heartbeat_at"] = in.LastHeartbeatAt
		out["heartbeat_expires_at"] = in.HeartbeatExpiresAt
	}
	if in.ClosedAt != "" {
		out["closed_at"] = in.ClosedAt
		out["close_outcome"] = in.CloseOutcome
	}
	return out
}

func writeIntentText(w interface {
	Write(p []byte) (n int, err error)
}, in domain.Intent, ipaths []domain.IntentPath) {
	fmt.Fprintf(w, "intent_id=%s\nstatus=%s\ntask=%s\nagent=%s\npolicy=%s\naccess=%s\n", in.IntentID, in.Status, in.TaskID, in.AgentID, in.ConflictPolicy, in.AccessMode)
	for _, p := range ipaths {
		fmt.Fprintf(w, "path=%s hash=%s\n", p.Path, p.PathHash)
	}
}

func staleIntents(intents []domain.Intent, now time.Time) []domain.Intent {
	out := make([]domain.Intent, 0)
	for _, in := range intents {
		if in.HeartbeatExpiresAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, in.HeartbeatExpiresAt)
		if err != nil {
			continue
		}
		if t.Before(now) {
			out = append(out, in)
		}
	}
	return out
}

func staleIDs(in []domain.Intent) []string {
	ids := make([]string, 0, len(in))
	for _, x := range in {
		ids = append(ids, x.IntentID)
	}
	return ids
}

// recentChanges returns up to 10 recent change rows. Phase 1 task 005
// does not own the changes table writers, but reading is safe.
func recentChanges(ctx context.Context, store *sqlite.Store, taskID string) []map[string]any {
	q := `SELECT change_id, COALESCE(intent_id, ''), task_id, COALESCE(agent_id, ''), summary, created_at FROM changes`
	args := []any{}
	if taskID != "" {
		q += " WHERE task_id = ?"
		args = append(args, taskID)
	}
	q += " ORDER BY created_at DESC LIMIT 10"
	rows, err := store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var cid, iid, tid, aid, summary, created string
		if err := rows.Scan(&cid, &iid, &tid, &aid, &summary, &created); err != nil {
			return out
		}
		out = append(out, map[string]any{
			"change_id":  cid,
			"intent_id":  iid,
			"task_id":    tid,
			"agent_id":   aid,
			"summary":    summary,
			"created_at": created,
		})
	}
	return out
}
