package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupUsesReadOnlyBillingWithoutPVZToken(t *testing.T) {
	var called atomic.Bool
	billing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		assert.Equal(t, "", r.Header.Get("Authorization"))
		assert.Equal(t, "/billing/rates", r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer billing.Close()
	a := apiClient{base: "http://127.0.0.1:1", billing: billing.URL, http: billing.Client()}
	var rates []any
	require.NoError(t, a.call(t.Context(), http.MethodGet, "/billing/rates", "", nil, &rates))
	assert.True(t, called.Load())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, a.call(ctx, http.MethodGet, "/billing/rates", "", nil, &rates), context.Canceled)
}
