package commands

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

func TestRecoverIfAbsentAssignmentBranchesOnLookupErrors(t *testing.T) {
	wanted := domain.Assignment{TaskID: "task-1", AssignedAgentID: "agent-1", OrchestratorID: "orch", ConflictPolicy: domain.PolicyWarn, Reason: "same"}
	lookup := func(context.Context, string, string) (domain.Assignment, error) {
		return domain.Assignment{}, errors.New("lookup blew up")
	}

	_, _, err := recoverIfAbsentAssignment(context.Background(), lookup, wanted, wanted.TaskID, wanted.AssignedAgentID)
	if err == nil {
		t.Fatal("expected lookup error")
	}
	ce, ok := err.(*cli.Error)
	if !ok {
		t.Fatalf("expected *cli.Error, got %T", err)
	}
	if ce.ExitCode != cli.ExitStorageIO || ce.Code != "assign_lookup_failed" {
		t.Fatalf("expected assign_lookup_failed storage error, got %+v", ce)
	}

	lookup = func(context.Context, string, string) (domain.Assignment, error) {
		return domain.Assignment{}, sql.ErrNoRows
	}
	_, _, err = recoverIfAbsentAssignment(context.Background(), lookup, wanted, wanted.TaskID, wanted.AssignedAgentID)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	ce, ok = err.(*cli.Error)
	if !ok {
		t.Fatalf("expected *cli.Error, got %T", err)
	}
	if ce.ExitCode != cli.ExitConflict || ce.Code != "assignment_exists" {
		t.Fatalf("expected assignment_exists conflict, got %+v", ce)
	}
}
