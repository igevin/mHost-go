package demo

import (
	"context"
	"errors"
	"strings"
	"testing"

	app "github.com/igevin/mHost-go/internal/app"
)

func TestHostsServiceListProfiles(t *testing.T) {
	service := NewHostsService()
	profiles, err := service.ListProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 4 {
		t.Fatalf("profiles = %+v", profiles)
	}
	byID := map[string]app.HostsProfile{}
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	if byID["dev"].RuleCount != 2 || byID["conflict"].RuleCount != 4 || byID["protected"].Protected != true {
		t.Fatalf("profiles = %+v", byID)
	}
	if byID["protected"].Revision == 0 {
		t.Fatalf("protected revision not set: %+v", byID["protected"])
	}
}

func TestHostsServiceSaveProfileRejectsStaleBase(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	detail, err := service.LoadProfile(ctx, "dev")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.SaveProfile(ctx, "dev", detail.Content+"# tail\n", detail.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != detail.Revision+1 {
		t.Fatalf("revision = %d, want %d", saved.Revision, detail.Revision+1)
	}
	if _, err := service.SaveProfile(ctx, "dev", saved.Content, detail.Revision); !errors.Is(err, app.ErrStaleOperation) {
		t.Fatalf("stale save err = %v", err)
	}
}

func TestHostsServicePreviewApplyCleanProfile(t *testing.T) {
	service := NewHostsService()
	plan, err := service.PreviewApply(context.Background(), "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID == "" || plan.ProfileID != "dev" || !plan.BackupRequired {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.CurrentBlock != initialBlock {
		t.Fatalf("current block = %q", plan.CurrentBlock)
	}
	if !strings.Contains(plan.MergedBlock, "api.dev.local") || strings.Contains(plan.MergedBlock, "old.dev.local") {
		t.Fatalf("merged block = %q", plan.MergedBlock)
	}
	if len(plan.Conflicts) != 0 || len(plan.Blockers) != 0 {
		t.Fatalf("conflicts/blockers = %+v/%+v", plan.Conflicts, plan.Blockers)
	}
}

func TestHostsServicePreviewApplyDetectsConflicts(t *testing.T) {
	service := NewHostsService()
	plan, err := service.PreviewApply(context.Background(), "conflict", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("blockers = %+v", plan.Blockers)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].Domain != "api.demo.test" {
		t.Fatalf("conflicts = %+v", plan.Conflicts)
	}
	conflict := plan.Conflicts[0]
	if len(conflict.Lines) != 2 || len(conflict.IPs) != 2 {
		t.Fatalf("conflict = %+v", conflict)
	}
	// same-IP duplicates merge silently and must not block
	if strings.Count(plan.MergedBlock, "shared.demo.test") != 1 {
		t.Fatalf("merged block = %q", plan.MergedBlock)
	}
}

func TestHostsServicePreviewRendersPendingResolutions(t *testing.T) {
	service := NewHostsService()
	plan, err := service.PreviewApply(context.Background(), "conflict", map[string]string{"api.demo.test": "192.168.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.MergedBlock, "192.168.1.1 api.demo.test") {
		t.Fatalf("merged block = %q", plan.MergedBlock)
	}
	if strings.Contains(plan.MergedBlock, "10.0.0.1 api.demo.test") {
		t.Fatalf("dropped conflict line still rendered: %q", plan.MergedBlock)
	}
}

func TestHostsServicePreviewApplyReportsBlockers(t *testing.T) {
	service := NewHostsService()
	plan, err := service.PreviewApply(context.Background(), "broken", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 1 || plan.Blockers[0].Message == "" {
		t.Fatalf("blockers = %+v", plan.Blockers)
	}
}

func TestHostsServiceInlineCommentsAreNotDomains(t *testing.T) {
	service := NewHostsService()
	plan, err := service.PreviewApply(context.Background(), "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.MergedBlock, "127.0.0.1 localhost\n") {
		t.Fatalf("merged block = %q", plan.MergedBlock)
	}
	if strings.Contains(plan.MergedBlock, "loopback") {
		t.Fatalf("inline comment leaked into merged block: %q", plan.MergedBlock)
	}
}

func TestHostsServiceApplySuccess(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	plan, err := service.PreviewApply(ctx, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Apply(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RollbackRef == nil {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(receipt.Results) != 3 {
		t.Fatalf("results = %+v", receipt.Results)
	}
	for _, result := range receipt.Results {
		if result.Status != app.WriteWritten {
			t.Fatalf("result = %+v", result)
		}
		if result.Entity == "verification" && !result.ReadbackVerified {
			t.Fatalf("verification not verified: %+v", result)
		}
	}
	applied, err := service.AppliedState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if applied.ProfileID != "dev" || applied.Drifted {
		t.Fatalf("applied = %+v", applied)
	}
	backups, err := service.ListApplyBackups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].BlockContent != initialBlock {
		t.Fatalf("backups = %+v", backups)
	}
}

func TestHostsServiceApplyRejectsUnresolvedConflicts(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	plan, err := service.PreviewApply(ctx, "conflict", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(ctx, plan.ID, nil)
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("err = %v", err)
	}
	receipt, err := service.Apply(ctx, plan.ID, map[string]string{"api.demo.test": "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Results) != 3 {
		t.Fatalf("results = %+v", receipt.Results)
	}
	applied, _ := service.AppliedState(ctx)
	if applied.ProfileID != "conflict" {
		t.Fatalf("applied = %+v", applied)
	}
}

func TestHostsServiceApplyRejectsInvalidResolution(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	plan, err := service.PreviewApply(ctx, "conflict", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(ctx, plan.ID, map[string]string{"api.demo.test": "203.0.113.1"})
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorValidation {
		t.Fatalf("err = %v", err)
	}
}

func TestHostsServiceApplyRejectsStalePlan(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	plan, err := service.PreviewApply(ctx, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.LoadProfile(ctx, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveProfile(ctx, "dev", detail.Content+"# edit\n", detail.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, plan.ID, nil); !errors.Is(err, app.ErrStaleOperation) {
		t.Fatalf("stale apply err = %v", err)
	}
}

func TestHostsServiceApplyFailureOffersRollback(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	plan, err := service.PreviewApply(ctx, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	service.FailNextApply()
	receipt, err := service.Apply(ctx, plan.ID, nil)
	if err == nil {
		t.Fatal("expected apply error")
	}
	if receipt.RollbackRef == nil {
		t.Fatalf("receipt = %+v", receipt)
	}
	statuses := map[string]app.WriteStatus{}
	for _, result := range receipt.Results {
		statuses[result.Entity] = result.Status
	}
	if statuses["backup"] != app.WriteWritten || statuses["managed_block"] != app.WriteFailed {
		t.Fatalf("statuses = %+v", statuses)
	}
	applied, _ := service.AppliedState(ctx)
	if applied.ProfileID != "protected" {
		t.Fatalf("applied = %+v", applied)
	}

	rollback, err := service.Rollback(ctx, receipt.RollbackRef.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range rollback.Results {
		if result.Status != app.WriteWritten {
			t.Fatalf("rollback result = %+v", result)
		}
		if result.Entity == "verification" && !result.ReadbackVerified {
			t.Fatalf("rollback verification not verified: %+v", result)
		}
	}
	applied, _ = service.AppliedState(ctx)
	if applied.ProfileID != "protected" || applied.Drifted {
		t.Fatalf("applied after rollback = %+v", applied)
	}
}

func TestHostsServiceReadbackMismatchIsReported(t *testing.T) {
	service := NewHostsService()
	ctx := context.Background()
	plan, err := service.PreviewApply(ctx, "dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	service.FailNextReadback()
	receipt, err := service.Apply(ctx, plan.ID, nil)
	if err == nil {
		t.Fatal("expected verification error")
	}
	var verification *app.ApplyResult
	for i := range receipt.Results {
		if receipt.Results[i].Entity == "verification" {
			verification = &receipt.Results[i]
		}
	}
	if verification == nil || verification.ReadbackVerified || verification.Issue == nil {
		t.Fatalf("verification = %+v", verification)
	}
	if receipt.RollbackRef == nil {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestHostsServiceAppliedStateDrift(t *testing.T) {
	service := NewHostsService()
	service.SetDrift(true)
	applied, err := service.AppliedState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Drifted || applied.ProfileID != "protected" {
		t.Fatalf("applied = %+v", applied)
	}
}

func TestHostsServiceRollbackUnknownBackup(t *testing.T) {
	service := NewHostsService()
	_, err := service.Rollback(context.Background(), "nope")
	var uiErr *app.UIError
	if !errors.As(err, &uiErr) || uiErr.Kind != app.ErrorNotFound {
		t.Fatalf("err = %v", err)
	}
}
