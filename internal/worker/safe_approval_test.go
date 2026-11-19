package worker

import (
	"math/big"
	"strings"
	"testing"

	"github.com/cosmo-local-credit/clc-core/internal/safe"
	"github.com/cosmo-local-credit/clc-core/pkg/event"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestSafeApprovalStage(t *testing.T) {
	tests := []struct {
		index, required int
		kind            string
		proposed        bool
	}{
		{0, 2, event.SafeProposed, true},
		{1, 3, event.SafeConfirmed, false},
		{2, 3, event.SafeApproved, false},
		{0, 1, event.SafeApproved, true},
		{3, 3, event.SafeConfirmed, false},
	}
	for _, test := range tests {
		kind, proposed := safeApprovalStage(test.index, test.required)
		require.Equal(t, test.kind, kind)
		require.Equal(t, test.proposed, proposed)
	}
}

func TestSafeApprovalTargetsDeduplicatesAndRecognizesSelfManagement(t *testing.T) {
	safeAddress := common.HexToAddress("0x1111111111111111111111111111111111111111")
	asset := common.HexToAddress("0x2222222222222222222222222222222222222222")
	policy := safe.NewPolicy(nil, nil, safe.DefaultAllowedSignatures)
	targets, allOwned := safeApprovalTargets(safeAddress, &safe.Exec{
		To: safeAddress, Value: big.NewInt(0), Data: []byte{1}, Operation: 0,
	}, policy)
	require.True(t, allOwned)
	require.Empty(t, targets)

	targets, allOwned = safeApprovalTargets(safeAddress, &safe.Exec{
		To: asset, Value: big.NewInt(0), Data: []byte{1}, Operation: 0,
	}, policy)
	require.False(t, allOwned)
	require.Equal(t, []string{strings.ToLower(asset.Hex())}, targets)
}
