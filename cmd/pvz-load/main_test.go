package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigRejectsUnboundedOrMisleadingLoad(t *testing.T) {
	for _, args := range [][]string{
		{"-url", "https://user:secret@example.org"}, {"-url", "https://example.org/api"}, {"-billing-url", "file:///tmp/x"},
		{"-rps", "0"}, {"-duration", "0s"}, {"-duration", "2m"}, {"-workers", "0"}, {"-writers", "0"},
		{"-warmup", "-1s"}, {"-warmup", "31s"},
		{"-workers", "5", "-writers", "10"}, {"-rps", "9", "-writers", "10"},
		{"-rps", "10000", "-duration", "30s", "-writers", "10"}, {"-profile", "unknown"},
	} {
		_, err := parse(args)
		assert.Error(t, err, args)
	}
	c, err := parse([]string{"-url", "https://pvz.example.org/", "-rps", "40", "-profile", "list"})
	require.NoError(t, err)
	assert.Equal(t, "https://pvz.example.org", c.base)
	assert.Equal(t, c.base, c.billing)
	assert.Equal(t, 30*time.Second, c.duration)
}

func TestFailedRunRemovesStaleSuccessReport(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "unused-cli")
	require.NoError(t, os.WriteFile(binary, nil, 0o600))
	summary := filepath.Join(dir, "summary.json")
	require.NoError(t, os.WriteFile(summary, []byte(`{"passed":true}`), 0o600))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	err := run(t.Context(), []string{"-url", srv.URL, "-loadgen-bin", binary, "-out", dir}, io.Discard)
	require.ErrorContains(t, err, "HTTP 503")
	_, err = os.Stat(summary)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
