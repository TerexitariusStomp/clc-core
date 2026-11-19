package sub

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cosmo-local-credit/clc-core/internal/store"
	"github.com/cosmo-local-credit/clc-core/internal/worker"
	"github.com/ethereum/go-ethereum/common"
	trackerEvent "github.com/grassrootseconomics/eth-tracker/pkg/event"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
)

func TestPoolVoucherAdmissionQueuesCurrentOwnersOncePerTransition(t *testing.T) {
	dsn := os.Getenv("CORE_TEST_DSN")
	if dsn == "" {
		t.Skip("CORE_TEST_DSN not set")
	}
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	pg, err := store.NewPgStore(store.PgOpts{
		Logg: logger, DSN: dsn, MigrationsFolderPath: "../../migrations", QueriesFolderPath: "../../core_queries.sql",
	})
	require.NoError(t, err)
	t.Cleanup(pg.Pool().Close)
	driver := riverpgxv5.New(pg.Pool())
	migrator, err := rivermigrate.New(driver, nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	queue, err := river.NewClient(driver, &river.Config{})
	require.NoError(t, err)

	poolAddress := admissionAddress(t)
	voucherAddress := admissionAddress(t)
	registryAddress := admissionAddress(t)
	poolOwner := admissionAddress(t)
	voucherOwner := admissionAddress(t)
	for index, address := range []string{poolOwner, voucherOwner} {
		_, err = pg.Pool().Exec(ctx, `
			INSERT INTO users(blockchain_address,name,email,status)
			VALUES($1,$2,$3,'APPROVED')`, address, "Admission owner", strings.ToLower(address)+"@example.invalid")
		require.NoError(t, err, index)
	}
	_, err = pg.Pool().Exec(ctx, `
		INSERT INTO vouchers(chain_id,address,name,symbol,owner_address,active)
		VALUES(100,$1,$2,$3,$4,true)`, voucherAddress, "Admission Voucher "+voucherAddress,
		"AV"+voucherAddress[len(voucherAddress)-6:], voucherOwner)
	require.NoError(t, err)
	_, err = pg.Pool().Exec(ctx, `
		INSERT INTO pools(chain_id,address,name,symbol,owner_address,token_registry_address,active)
		VALUES(100,$1,$2,$3,$4,$5,true)`, poolAddress, "Admission Pool "+poolAddress,
		"AP"+poolAddress[len(poolAddress)-6:], poolOwner, registryAddress)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := pg.Pool().Exec(ctx, `DELETE FROM river_job
			WHERE kind='MESSAGING_EVENT' AND args->>'kind'='pool.voucher_admitted'
			AND args->'resource'->>'address'=$1`, poolAddress)
		require.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool().Exec(ctx, `DELETE FROM pools WHERE chain_id=100 AND address=$1`, poolAddress)
		require.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool().Exec(ctx, `DELETE FROM vouchers WHERE chain_id=100 AND address=$1`, voucherAddress)
		require.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool().Exec(ctx, `DELETE FROM users WHERE blockchain_address=ANY($1::TEXT[])`, []string{poolOwner, voucherOwner})
		require.NoError(t, cleanupErr)
	})

	s := &Sub{store: pg, chainID: 100, queueClient: queue, logg: logger}
	project := func(txType, txHash string, block uint64) {
		t.Helper()
		raw, marshalErr := json.Marshal(trackerEvent.Event{
			Success: true, TxType: txType, TxHash: txHash, ContractAddress: registryAddress,
			Block: block, Index: 1, Timestamp: uint64(time.Now().Unix()),
			Payload: map[string]any{"address": voucherAddress},
		})
		require.NoError(t, marshalErr)
		require.NoError(t, s.processEvent(ctx, "TRACKER."+txType, raw))
	}
	countJobs := func() int {
		t.Helper()
		var count int
		require.NoError(t, pg.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM river_job
			WHERE kind='MESSAGING_EVENT' AND args->>'kind'='pool.voucher_admitted'
			AND args->'resource'->>'address'=$1`, poolAddress).Scan(&count))
		return count
	}

	project(indexAddEvent, "0x"+strings.Repeat("1", 64), 10)
	require.Equal(t, 2, countJobs())
	var raw []byte
	require.NoError(t, pg.Pool().QueryRow(ctx, `SELECT args FROM river_job
		WHERE kind='MESSAGING_EVENT' AND args->>'kind'='pool.voucher_admitted'
		AND args->'resource'->>'address'=$1 LIMIT 1`, poolAddress).Scan(&raw))
	var args worker.MessagingEventArgs
	require.NoError(t, json.Unmarshal(raw, &args))
	require.Equal(t, poolAddress, args.Resource.Address)
	require.Equal(t, voucherAddress, args.Resource.VoucherAddress)
	require.Len(t, args.Audience, 1)

	// A replay and a newer true -> true projection do not create another alert.
	project(indexAddEvent, "0x"+strings.Repeat("1", 64), 10)
	project(indexAddEvent, "0x"+strings.Repeat("2", 64), 11)
	require.Equal(t, 2, countJobs())

	// Removing and later readmitting is a new authoritative transition.
	project(indexRemoveEvent, "0x"+strings.Repeat("3", 64), 12)
	project(indexAddEvent, "0x"+strings.Repeat("4", 64), 13)
	require.Equal(t, 4, countJobs())
}

func admissionAddress(t *testing.T) string {
	t.Helper()
	var raw [20]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	return common.BytesToAddress(raw[:]).Hex()
}
