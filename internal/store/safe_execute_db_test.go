package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
)

type (
	// safeExecuteJob restates the worker's job: store tests cannot import worker.
	safeExecuteJob struct {
		SafeTxHash string `json:"safeTxHash"`
		Safe       string `json:"safe"`
	}
)

func (safeExecuteJob) Kind() string { return "SAFE_EXECUTE" }

func TestSafeExecutionProgressesDistinguishActiveAndFinalJobs(t *testing.T) {
	pg, tx, cleanup := newMarketTestPg(t)
	defer cleanup()
	ctx := context.Background()
	// The market fixture has no pool; River's migrator and client need one.
	pool, err := pgxpool.New(ctx, os.Getenv("CORE_TEST_DSN"))
	require.NoError(t, err)
	defer pool.Close()
	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	queue, err := river.NewClient(driver, &river.Config{})
	require.NoError(t, err)

	queued, cancelled, completed, unknown := randomHash(t), randomHash(t), randomHash(t), randomHash(t)
	for _, hash := range []string{queued, cancelled, completed} {
		_, err := queue.InsertTx(ctx, tx, safeExecuteJob{SafeTxHash: hash, Safe: "0x1111111111111111111111111111111111111111"}, nil)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `UPDATE river_job SET state='cancelled', finalized_at=now() WHERE kind='SAFE_EXECUTE' AND args->>'safeTxHash'=$1`, cancelled)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE river_job SET state='completed', finalized_at=now() WHERE kind='SAFE_EXECUTE' AND args->>'safeTxHash'=$1`, completed)
	require.NoError(t, err)

	hashes := []string{queued, cancelled, completed, unknown}
	tracking := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	got, err := pg.SafeExecutionProgresses(ctx, tx, hashes, tracking)
	require.NoError(t, err)
	require.Equal(t, "available", got[queued].JobState)
	require.Equal(t, "cancelled", got[cancelled].JobState)
	require.Equal(t, "completed", got[completed].JobState)
	require.Zero(t, got[unknown].JobID)
}

func randomHash(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return "0x" + hex.EncodeToString(b)
}
