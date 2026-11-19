package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cosmo-local-credit/clc-core/internal/safe"
	"github.com/cosmo-local-credit/clc-core/internal/store"
	apiresp "github.com/cosmo-local-credit/clc-core/pkg/api"
	"github.com/ethereum/go-ethereum/common"
	"github.com/labstack/echo/v5"
)

func TestSafeExecutionStatePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		progress store.SafeExecutionProgress
		want     safe.ExecutionState
	}{
		{name: "never attempted", want: safe.ExecutionReady},
		{name: "queued", progress: store.SafeExecutionProgress{JobState: "available"}, want: safe.ExecutionQueued},
		{name: "running", progress: store.SafeExecutionProgress{JobState: "running"}, want: safe.ExecutionQueued},
		{name: "submitted", progress: store.SafeExecutionProgress{JobState: "completed", OTXActive: true}, want: safe.ExecutionSubmitted},
		{name: "sent successfully while STS catches up", progress: store.SafeExecutionProgress{JobState: "completed", OTXSucceeded: true}, want: safe.ExecutionSubmitted},
		{name: "completed without submission", progress: store.SafeExecutionProgress{JobState: "completed"}, want: safe.ExecutionFailed},
		{name: "terminal OTX", progress: store.SafeExecutionProgress{OTXFailed: true}, want: safe.ExecutionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := safeExecutionState(test.progress); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

type (
	noSafes struct{}
)

func (noSafes) IsSafe(context.Context, common.Address) (bool, error) { return false, nil }
func (noSafes) ManagesAsset(context.Context, common.Address, common.Address) (bool, error) {
	return false, nil
}

func relayRequestWith(to, data string) apiresp.RelayRequest {
	return apiresp.RelayRequest{SignedBatch: apiresp.CaliburSignedBatch{
		BatchedCall: apiresp.CaliburBatchedCall{Calls: []apiresp.CaliburCall{{To: to, Value: "0", Data: data}}},
	}}
}

func enforce(t *testing.T, a *API, req apiresp.RelayRequest) (handled bool, status int) {
	t.Helper()
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")), rec)
	// A rejection writes its response and also returns an error; only a
	// request that was not handled should be error-free.
	handled, err := a.enforceSafePolicy(c, req)
	if err != nil && !handled {
		t.Fatal(err)
	}
	return handled, rec.Code
}

const (
	safeAt      = "0x1111111111111111111111111111111111111111"
	execGarbage = "0x6a761202deadbeef"
)

func TestRelayRejectsExecTransactionWhenSafeDisabled(t *testing.T) {
	handled, status := enforce(t, &API{}, relayRequestWith(safeAt, execGarbage))
	if !handled || status != http.StatusForbidden {
		t.Fatalf("handled=%v status=%d, want rejected 403", handled, status)
	}
}

func TestRelayRejectsMalformedExecTransactionWhenEnabled(t *testing.T) {
	a := &API{safePolicy: safe.NewPolicy(noSafes{}, nil, safe.DefaultAllowedSignatures)}
	handled, status := enforce(t, a, relayRequestWith(safeAt, execGarbage))
	if !handled || status != http.StatusForbidden {
		t.Fatalf("handled=%v status=%d, want rejected 403", handled, status)
	}
}

func TestRelayPassesBatchesThatDoNotTouchASafe(t *testing.T) {
	for name, a := range map[string]*API{
		"disabled": {},
		"enabled":  {safePolicy: safe.NewPolicy(noSafes{}, nil, safe.DefaultAllowedSignatures)},
	} {
		t.Run(name, func(t *testing.T) {
			handled, _ := enforce(t, a, relayRequestWith(safeAt, "0xa9059cbb"))
			if handled {
				t.Fatal("ordinary batch was intercepted")
			}
		})
	}
}
