package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cosmo-local-credit/clc-core/internal/safe"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type (
	inspectionReader struct {
		state *safe.State
		reads int
	}
)

func (r *inspectionReader) ReadSafeLatest(context.Context, common.Address) (*safe.State, uint64, error) {
	r.reads++
	return r.state, uint64(500 + r.reads), nil
}

func TestPublicOwnerInspectionPersistsCompatibleSignersWithoutSTS(t *testing.T) {
	a, _ := newOfferingAPI(t)
	a.chainID = 42220
	fx, cleanup := seedOfferingFixtures(t, a)
	defer cleanup()
	ctx := context.Background()
	const owner = "0x9137770000000000000000000000000000000001"
	_, err := a.store.Pool().Exec(ctx, `UPDATE vouchers SET owner_address=$2 WHERE id=$1`, fx.ownedID, owner)
	require.NoError(t, err)
	_, err = a.store.Pool().Exec(ctx, `INSERT INTO users(blockchain_address,name,email,username,status)
        VALUES($1,'Safe address account','safe-address-owner@example.test','safe-address-owner','APPROVED')`, owner)
	require.NoError(t, err)
	_, err = a.store.Pool().Exec(ctx, `INSERT INTO users(blockchain_address,name,email,username,avatar_url,status)
        VALUES($1,'Safe signer','safe-signer@example.test','safe-signer','https://storage.example/signer.webp','APPROVED')`, offeringOwnerAddress)
	require.NoError(t, err)
	reader := &inspectionReader{state: &safe.State{Owners: []common.Address{common.HexToAddress(offeringOwnerAddress)}}}
	a.safeReader = reader
	t.Cleanup(func() {
		_, _ = a.store.Pool().Exec(ctx, `DELETE FROM safe_state WHERE safe_address=$1`, owner)
		_, _ = a.store.Pool().Exec(ctx, `DELETE FROM users WHERE lower(blockchain_address) IN (lower($1),lower($2))`, owner, offeringOwnerAddress)
	})
	response, result := offeringRequest(t, a, http.MethodGet, "/api/v2/owners/"+owner, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "safe", result["kind"])
	require.Nil(t, result["threshold"])
	require.Equal(t, []any{strings.ToLower(offeringOwnerAddress)}, result["signers"])
	targets, ok := result["messageTargets"].([]any)
	require.True(t, ok)
	require.Len(t, targets, 1)
	target, ok := targets[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, common.HexToAddress(offeringOwnerAddress).Hex(), target["address"])
	require.Equal(t, "safe-signer", target["username"])
	require.Equal(t, "https://storage.example/signer.webp", target["avatar"])
	require.NotContains(t, target, "email")
	require.NotContains(t, target, "phone")
	require.Nil(t, a.safeProxy, "metadata must not require the transaction service")
	var count int
	require.NoError(t, a.store.Pool().QueryRow(ctx, `SELECT count(*) FROM safe_signers WHERE safe_address=$1`, owner).Scan(&count))
	require.Equal(t, 1, count)

	// Reinspection refreshes custom contracts even if they omit Safe events.
	reader.state.Owners = []common.Address{common.HexToAddress(offeringStrangerAddr)}
	response, result = offeringRequest(t, a, http.MethodGet, "/api/v2/owners/"+owner, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []any{strings.ToLower(offeringStrangerAddr)}, result["signers"])
}

func TestPublicOwnerMessageTargetsUseOnlyEligiblePublicIdentity(t *testing.T) {
	a, _ := newOfferingAPI(t)
	fx, cleanup := seedOfferingFixtures(t, a)
	defer cleanup()
	ctx := context.Background()
	_, err := a.store.Pool().Exec(ctx, `INSERT INTO users(blockchain_address,name,email,username,status)
        VALUES($1,'Direct owner','direct-owner@example.test','direct-owner','APPROVED')`, offeringOwnerAddress)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = a.store.Pool().Exec(ctx, `DELETE FROM users WHERE lower(blockchain_address)=lower($1)`, offeringOwnerAddress)
	})

	response, result := offeringRequest(t, a, http.MethodGet, "/api/v2/owners/"+offeringOwnerAddress, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	targets, ok := result["messageTargets"].([]any)
	require.True(t, ok)
	require.Len(t, targets, 1)
	target, ok := targets[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "direct-owner", target["username"])

	_, err = a.store.Pool().Exec(ctx, `UPDATE users SET status='REJECTED' WHERE lower(blockchain_address)=lower($1)`, offeringOwnerAddress)
	require.NoError(t, err)
	response, result = offeringRequest(t, a, http.MethodGet, "/api/v2/owners/"+offeringOwnerAddress, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, result["messageTargets"])
	_ = fx
}

func TestOwnerInspectionDoesNotRegisterArbitraryMultisigs(t *testing.T) {
	a, _ := newOfferingAPI(t)
	a.chainID = 42220
	reader := &inspectionReader{}
	a.safeReader = reader
	response, result := offeringRequest(t, a, http.MethodGet, "/api/v2/owners/0x7153330000000000000000000000000000000001", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "unsupported", result["kind"])
	require.Zero(t, reader.reads)
}
