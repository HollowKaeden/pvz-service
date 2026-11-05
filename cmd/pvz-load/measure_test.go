package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSLIDoesNotHideSlowOrMissingRequests(t *testing.T) {
	for _, tc := range []struct {
		name           string
		total, correct int
		p99            float64
		pass           bool
	}{
		{"meets target", 30000, 30000, 20, true},
		{"rate fell despite low latency", 15000, 15000, 10, false},
		{"tail too slow", 30000, 30000, 101, false},
		{"too many errors", 30000, 29996, 10, false},
		{"within stated error budget", 30000, 29999, 10, true},
		{"empty sample", 0, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := cliReport{Total: tc.total, ByStatus: map[string]int{"200": tc.correct}}
			r.Latency.P99 = tc.p99
			got := summarize("list", []cliReport{r}, []loadJob{{expectedStatus: http.StatusOK}}, 30*time.Second, 1000)
			assert.Equal(t, tc.pass, got.Passed)
			assert.Equal(t, tc.total-tc.correct, got.Failed)
		})
	}
}

func TestParallelReportsUseWorstTailAndWallTime(t *testing.T) {
	a := cliReport{Total: 15000, ByStatus: map[string]int{"201": 15000}}
	a.Latency.P99 = 10
	b := cliReport{Total: 15000, ByStatus: map[string]int{"201": 14999, "200": 1}}
	b.Latency.P99 = 150
	jobs := []loadJob{{expectedStatus: http.StatusCreated}, {expectedStatus: http.StatusCreated}}
	r := summarize("products", []cliReport{a, b}, jobs, 30*time.Second, 1000)
	assert.Equal(t, 150.0, r.MaxStreamP99)
	assert.Equal(t, 1000.0, r.RPS)
	assert.Equal(t, 29999, r.OK)
	assert.Equal(t, 1, r.Failed)
	assert.False(t, r.Passed)
	assert.False(t, summarize("bad", []cliReport{a}, nil, time.Second, 1000).Passed)
}
