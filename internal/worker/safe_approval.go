package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cosmo-local-credit/clc-core/internal/safe"
	"github.com/cosmo-local-credit/clc-core/pkg/event"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

const (
	safeApprovalReconcileInterval = 30 * time.Second
	safeApprovalAudienceChunk     = 16
)

var (
	safeApprovalEventNamespace = uuid.MustParse("48b74931-52dc-4c55-a884-1b14e68893c1")
)

type (
	safeApprovalSTS interface {
		Transaction(context.Context, string) (*safe.Transaction, error)
		Nonce(context.Context, string) (string, error)
		Queued(context.Context, string, string) ([]safe.Transaction, error)
	}

	SafeApprovalSyncArgs struct {
		SafeAddress string `json:"safeAddress"`
		SafeTxHash  string `json:"safeTxHash,omitempty"`
	}

	SafeApprovalReconcileArgs struct{}

	SafeApprovalSyncWorker struct {
		river.WorkerDefaults[SafeApprovalSyncArgs]

		wc     *WorkerContainer
		sts    safeApprovalSTS
		policy *safe.Policy
	}

	SafeApprovalReconcileWorker struct {
		river.WorkerDefaults[SafeApprovalReconcileArgs]

		wc *WorkerContainer
	}

	workerSafeResolver struct{ wc *WorkerContainer }
)

func (SafeApprovalSyncArgs) Kind() string      { return "SAFE_APPROVAL_SYNC" }
func (SafeApprovalReconcileArgs) Kind() string { return "SAFE_APPROVAL_RECONCILE" }

func EnqueueSafeApprovalSync(ctx context.Context, queue *river.Client[pgx.Tx], safeAddress, safeTxHash string) error {
	if queue == nil {
		return errors.New("safe approval sync: no queue client")
	}
	if !common.IsHexAddress(safeAddress) {
		return errors.New("safe approval sync: invalid Safe address")
	}
	if safeTxHash != "" && len(safeTxHash) != 66 {
		return errors.New("safe approval sync: invalid transaction hash")
	}
	_, err := queue.Insert(ctx, SafeApprovalSyncArgs{
		SafeAddress: common.HexToAddress(safeAddress).Hex(),
		SafeTxHash:  strings.ToLower(safeTxHash),
	}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

func (r workerSafeResolver) IsSafe(ctx context.Context, address common.Address) (bool, error) {
	tx, err := r.wc.store.Pool().Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	_, err = r.wc.store.GetSafeState(ctx, tx, r.wc.chainID, address.Hex())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r workerSafeResolver) ManagesAsset(ctx context.Context, safeAddress, target common.Address) (bool, error) {
	tx, err := r.wc.store.Pool().Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	return r.wc.store.SafeManagesAsset(ctx, tx, r.wc.chainID, safeAddress.Hex(), target.Hex())
}

func (w *SafeApprovalReconcileWorker) Work(ctx context.Context, _ *river.Job[SafeApprovalReconcileArgs]) error {
	tx, err := w.wc.store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	states, err := w.wc.store.ListSafeStates(ctx, tx, w.wc.chainID)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, state := range states {
		if err := EnqueueSafeApprovalSync(ctx, w.wc.queueClient, state.Address, ""); err != nil {
			return err
		}
	}
	return nil
}

func (w *SafeApprovalSyncWorker) Work(ctx context.Context, job *river.Job[SafeApprovalSyncArgs]) error {
	if job.Args.SafeTxHash != "" {
		tx, err := w.sts.Transaction(ctx, job.Args.SafeTxHash)
		if err != nil {
			return err
		}
		return w.syncTransaction(ctx, tx)
	}
	nonce, err := w.sts.Nonce(ctx, job.Args.SafeAddress)
	if err != nil {
		return err
	}
	txs, err := w.sts.Queued(ctx, job.Args.SafeAddress, nonce)
	if err != nil {
		return err
	}
	for i := range txs {
		if err := w.syncTransaction(ctx, &txs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (w *SafeApprovalSyncWorker) syncTransaction(ctx context.Context, safeTx *safe.Transaction) error {
	if safeTx == nil || !common.IsHexAddress(safeTx.Safe) || safeTx.SafeTxHash == "" || safeTx.ConfirmationsRequired < 1 {
		return nil
	}
	exec, err := safeTx.Exec()
	if err != nil {
		return nil
	}
	if err := w.policy.CheckTransaction(ctx, common.HexToAddress(safeTx.Safe), exec); err != nil {
		if errors.Is(err, safe.ErrRejected) {
			return nil
		}
		return err
	}
	targets, allOwned := safeApprovalTargets(common.HexToAddress(safeTx.Safe), exec, w.policy)
	confirmations := append([]safe.Confirmation(nil), safeTx.Confirmations...)
	sort.SliceStable(confirmations, func(i, j int) bool {
		if confirmations[i].SubmittedAt == confirmations[j].SubmittedAt {
			return strings.ToLower(confirmations[i].Owner) < strings.ToLower(confirmations[j].Owner)
		}
		return confirmations[i].SubmittedAt < confirmations[j].SubmittedAt
	})

	tx, err := w.wc.store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	audience, err := w.wc.store.ListOwnerMessageTargets(ctx, tx, w.wc.chainID, safeTx.Safe)
	if err != nil {
		return err
	}
	resources, err := w.wc.store.ListSafeApprovalResources(ctx, tx, w.wc.chainID, safeTx.Safe, targets, allOwned)
	if err != nil {
		return err
	}
	if len(audience) == 0 || len(resources) == 0 {
		return tx.Commit(ctx)
	}

	for index, confirmation := range confirmations {
		if !common.IsHexAddress(confirmation.Owner) {
			continue
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, confirmation.SubmittedAt)
		if err != nil {
			continue
		}
		baseID := uuid.NewSHA1(safeApprovalEventNamespace, fmt.Appendf(nil, "%d:%s:%s", w.wc.chainID, strings.ToLower(safeTx.SafeTxHash), strings.ToLower(confirmation.Owner)))
		emit, err := w.wc.store.RecordSafeApprovalMilestone(ctx, tx, w.wc.chainID, safeTx.SafeTxHash, confirmation.Owner, baseID.String(), occurredAt)
		if err != nil {
			return err
		}
		if !emit {
			continue
		}
		kind, proposed := safeApprovalStage(index, safeTx.ConfirmationsRequired)
		actorID := int64(0)
		for _, member := range audience {
			if strings.EqualFold(member.Address, confirmation.Owner) {
				actorID = member.UserID
				break
			}
		}
		for start := 0; start < len(audience); start += safeApprovalAudienceChunk {
			end := min(start+safeApprovalAudienceChunk, len(audience))
			chunk := make([]event.MessagingAudience, 0, end-start)
			for _, member := range audience[start:end] {
				chunk = append(chunk, event.MessagingAudience{UserID: member.UserID, Address: common.HexToAddress(member.Address).Hex(), Push: true})
			}
			eventID := uuid.NewSHA1(baseID, fmt.Appendf(nil, "audience:%d", start)).String()
			ev := event.MessagingEvent{
				EventID:    eventID,
				Kind:       kind,
				OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
				Actor:      event.MessagingActor{UserID: actorID, Address: common.HexToAddress(confirmation.Owner).Hex()},
				Audience:   chunk,
				SafeApproval: &event.MessagingSafeApproval{
					SafeAddress: common.HexToAddress(safeTx.Safe).Hex(), SafeTxHash: strings.ToLower(safeTx.SafeTxHash),
					Stage: kind, Approvals: index + 1, ApprovalsRequired: safeTx.ConfirmationsRequired, Proposed: proposed,
				},
			}
			for _, resource := range resources {
				ev.Resources = append(ev.Resources, event.MessagingResource{Kind: resource.Kind, Address: common.HexToAddress(resource.Address).Hex(), Name: resource.Name})
			}
			if err := EnqueueMessagingEvent(ctx, w.wc.queueClient, tx, ev); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func safeApprovalStage(index, required int) (string, bool) {
	if index+1 == required {
		return event.SafeApproved, index == 0
	}
	if index == 0 {
		return event.SafeProposed, true
	}
	return event.SafeConfirmed, false
}

func safeApprovalTargets(safeAddress common.Address, exec *safe.Exec, policy *safe.Policy) ([]string, bool) {
	calls := []safe.SubCall{{Operation: exec.Operation, To: exec.To, Value: exec.Value, Data: exec.Data}}
	if exec.Operation == safe.OperationDelegateCall {
		if _, ok := policy.MultiSend[exec.To]; ok {
			if decoded, err := safe.DecodeMultiSend(exec.Data); err == nil {
				calls = decoded
			}
		}
	}
	seen := map[string]struct{}{}
	targets := make([]string, 0, len(calls))
	allOwned := false
	for _, call := range calls {
		if call.To == safeAddress {
			allOwned = true
			continue
		}
		address := strings.ToLower(call.To.Hex())
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		targets = append(targets, address)
	}
	return targets, allOwned
}
