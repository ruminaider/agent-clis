package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

type initOpts struct {
	projectID     string
	ledgerDir     string
	writePointer  bool
	defaultTaskID string
}

func newInitCommand(streams IOStreams, _ *rootFlags) *cobra.Command {
	o := &initOpts{}
	cmd := &cobra.Command{
		Use:           "init",
		Short:         commandShortDescriptions()["init"],
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(streams, *o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.projectID, "project-id", "", "Explicit project identifier; persisted in pointer when --write-pointer is set")
	f.StringVar(&o.ledgerDir, "ledger-dir", "", "Override ledger directory (otherwise resolved per SPEC §8)")
	f.BoolVar(&o.writePointer, "write-pointer", false, "Write the local .agent-ledger.toml pointer (and best-effort git common-dir pointer)")
	f.StringVar(&o.defaultTaskID, "default-task-id", "", "Persist a default task id in the pointer for adapter session bootstrap (used when no harness-derived task id is available); requires --write-pointer")
	return cmd
}

func runInit(streams IOStreams, o initOpts) error {
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

	layout, err := storage.EnsureLayout(res.LedgerDir)
	if err != nil {
		return NewError(ExitStorageIO, "layout_create_failed", err.Error())
	}

	if o.defaultTaskID != "" && !o.writePointer {
		return NewError(ExitUsage, "default_task_id_requires_write_pointer", "--default-task-id requires --write-pointer")
	}
	if o.writePointer {
		if err := writePointer(res, o.defaultTaskID); err != nil {
			return NewError(ExitStorageIO, "pointer_write_failed", err.Error())
		}
		if err := writeGitPointer(res); err != nil {
			// Non-fatal: discoverability only.
			fmt.Fprintf(streams.Err, "warning: git common-dir pointer not written: %v\n", err)
		}
	}

	fmt.Fprintf(streams.Out, "ledger initialized at %s\n", layout.Dir)
	fmt.Fprintf(streams.Out, "project_id=%s slug=%s fingerprint=%s\n", res.Identity.ProjectID, res.Identity.Slug, res.Identity.Fingerprint)
	return nil
}

func writePointer(res project.Resolution, defaultTaskID string) error {
	p := config.Pointer{
		Version:   config.PointerVersion,
		ProjectID: res.Identity.ProjectID,
		LedgerDir: res.LedgerDir,
	}
	if res.Pointer != nil && res.Pointer.PolicyFile != "" {
		p.PolicyFile = res.Pointer.PolicyFile
	}
	// Carry forward an existing default_task_id if --default-task-id was
	// not supplied, so reruns of `init --write-pointer` do not silently
	// erase a previously declared value.
	if defaultTaskID != "" {
		p.DefaultTaskID = defaultTaskID
	} else if res.Pointer != nil {
		p.DefaultTaskID = res.Pointer.DefaultTaskID
	}
	return config.WritePointer(res.Root, p)
}

// writeGitPointer creates a discoverability pointer under the git common
// dir. It prefers a symlink, falling back to a pointer.toml file when
// symlinks fail (e.g. on Windows without privilege).
func writeGitPointer(res project.Resolution) error {
	if !res.GitInfo.IsRepo || res.GitInfo.CommonDir == "" {
		return nil
	}
	link := filepath.Join(res.GitInfo.CommonDir, project.GitPointerName)
	// Remove any prior pointer (file, dir, or symlink) before writing.
	_ = os.Remove(link)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(res.LedgerDir, link); err == nil {
			return nil
		}
	}
	// Fallback: write a pointer.toml file inside a directory of the same
	// name. This keeps the discoverability path stable per SPEC §8.3.
	if err := os.MkdirAll(link, 0o755); err != nil {
		return err
	}
	return writeFallbackPointerFile(filepath.Join(link, project.GitPointerFallbackName), res)
}

func writeFallbackPointerFile(path string, res project.Resolution) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// We hand-write a minimal TOML rather than reuse config.WritePointer
	// to avoid the .agent-ledger.toml filename convention here.
	_, err = fmt.Fprintf(f, "version = %d\nproject_id = %q\nledger_dir = %q\n",
		config.PointerVersion, res.Identity.ProjectID, res.LedgerDir)
	return err
}
