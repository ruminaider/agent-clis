package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
)

type identifyOpts struct {
	env       envOpener
	agentKind string
	harness   string
	parent    string
	model     string
	agentID   string
	shell     bool
	asJSON    bool
}

// NewIdentifyCommand returns the cobra command implementing SPEC §18.2.
func NewIdentifyCommand(streams Streams) *cobra.Command {
	o := &identifyOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "identify",
		Short:         "Create or print an agent session identity",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runIdentify(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.agentKind, "agent-kind", "", "Agent kind (e.g. worker, main, gate-reviewer)")
	f.StringVar(&o.harness, "harness", "", "Agent harness name (e.g. pi, claude-code)")
	f.StringVar(&o.parent, "parent", "", "Parent agent ID")
	f.StringVar(&o.model, "model", "", "Model identifier")
	f.StringVar(&o.agentID, "agent", "", "Override the chosen agent ID")
	f.BoolVar(&o.shell, "shell", false, "Print shell export lines for AGENT_ID and friends")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runIdentify(streams Streams, o *identifyOpts) error {
	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	if o.agentKind == "" {
		o.agentKind = strings.TrimSpace(os.Getenv("AGENT_KIND"))
	}
	if o.harness == "" {
		o.harness = strings.TrimSpace(os.Getenv("AGENT_HARNESS"))
	}

	if o.agentID == "" {
		o.agentID = strings.TrimSpace(os.Getenv("AGENT_ID"))
	}
	if o.agentID == "" {
		// Mint a new agent ID: <harness>.<agent-kind>.<short>
		harness := nonEmpty(o.harness, "local")
		kind := nonEmpty(o.agentKind, "worker")
		short, serr := shortID()
		if serr != nil {
			return cli.NewError(cli.ExitGeneric, "id_failed", serr.Error())
		}
		o.agentID = fmt.Sprintf("%s.%s.%s", harness, kind, short)
	}
	if o.agentKind == "" {
		o.agentKind = "worker"
	}

	if err := d.UpsertAgent(ctx, domain.Agent{
		AgentID:        o.agentID,
		AgentKind:      o.agentKind,
		Harness:        o.harness,
		Model:          o.model,
		ParentAgentID:  o.parent,
		OrchestratorID: strings.TrimSpace(os.Getenv("AGENT_ORCHESTRATOR_ID")),
		StartedAt:      id.FormatTimestamp(store.Clock()()),
	}); err != nil {
		return cli.NewError(cli.ExitStorageIO, "agent_upsert_failed", err.Error())
	}

	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"agent_id":   o.agentID,
			"agent_kind": o.agentKind,
			"harness":    o.harness,
			"parent":     o.parent,
		})
	}
	if o.shell {
		fmt.Fprintf(streams.Out, "export AGENT_ID=%s\n", shellQuote(o.agentID))
		fmt.Fprintf(streams.Out, "export AGENT_KIND=%s\n", shellQuote(o.agentKind))
		if o.harness != "" {
			fmt.Fprintf(streams.Out, "export AGENT_HARNESS=%s\n", shellQuote(o.harness))
		}
		if o.parent != "" {
			fmt.Fprintf(streams.Out, "export AGENT_PARENT_ID=%s\n", shellQuote(o.parent))
		}
		return nil
	}
	fmt.Fprintf(streams.Out, "agent_id=%s kind=%s harness=%s\n", o.agentID, o.agentKind, o.harness)
	return nil
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func shortID() (string, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n\"'\\$`") {
		// Single-quote and escape any embedded single quotes.
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}
