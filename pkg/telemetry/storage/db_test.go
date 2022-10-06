package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/textileio/go-tableland/pkg/telemetry"
	"github.com/textileio/go-tableland/pkg/telemetry/publisher"

	"github.com/textileio/go-tableland/tests"
)

func TestCollectAndFetchAndPublish(t *testing.T) {
	t.Parallel()

	// Notes:
	// This can't be wired per sub-tests for two reasons:
	// 1- `telemetry.SetMetricStore(...)` is a global setup at the package level, and
	// 2- `SetMetricStore(...)` has a `sync.Once` wrapping so can't be called more than once, so each sub-test can't
	//     override their value.
	//
	// This also means that sub-tests can't run in parallel.
	dbURI := tests.Sqlite3URI(t)
	s, err := New(dbURI)
	require.NoError(t, err)
	telemetry.SetMetricStore(s)

	t.Run("state hash", func(t *testing.T) {
		// collect two mocked statehash metrics
		require.NoError(t, telemetry.Collect(context.Background(), stateHash{}))
		require.NoError(t, telemetry.Collect(context.Background(), stateHash{}))

		metrics, err := s.FetchUnpublishedMetrics(context.Background(), 10)
		require.NoError(t, err)
		require.Len(t, metrics, 2)

		for _, metric := range metrics {
			require.Equal(t, telemetry.StateHashType, metric.Type)
			require.Equal(t, 1, metric.Version)
			require.False(t, metric.Timestamp.IsZero())

			sh := metric.Payload.(*telemetry.StateHashMetric)
			require.Equal(t, stateHash{}.ChainID(), sh.ChainID)
			require.Equal(t, stateHash{}.BlockNumber(), sh.BlockNumber)
			require.Equal(t, stateHash{}.Hash(), sh.Hash)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		exporter, err := publisher.NewHTTPExporter(ts.URL, "")
		require.NoError(t, err)
		nodeID := strings.Replace(uuid.NewString(), "-", "", -1)
		p := publisher.NewPublisher(s, exporter, nodeID, time.Second)
		p.Start()

		require.Eventually(t, func() bool {
			metrics, err = s.FetchUnpublishedMetrics(context.Background(), 2)
			require.NoError(t, err)
			return len(metrics) == 0
		}, 5*time.Second, time.Second)

		p.Close()
	})

	t.Run("git summary", func(t *testing.T) {
		// collect two mocked gitSummary metrics
		require.NoError(t, telemetry.Collect(context.Background(), gitSummary{}))
		require.NoError(t, telemetry.Collect(context.Background(), gitSummary{}))

		metrics, err := s.FetchUnpublishedMetrics(context.Background(), 10)
		require.NoError(t, err)
		require.Len(t, metrics, 2)

		for _, metric := range metrics {
			require.Equal(t, telemetry.GitSummaryType, metric.Type)
			require.Equal(t, 1, metric.Version)
			require.False(t, metric.Timestamp.IsZero())

			gv := metric.Payload.(*telemetry.GitSummaryMetric)
			require.Equal(t, gitSummary{}.GetGitCommit(), gv.GitCommit)
			require.Equal(t, gitSummary{}.GetGitBranch(), gv.GitBranch)
			require.Equal(t, gitSummary{}.GetGitState(), gv.GitState)
			require.Equal(t, gitSummary{}.GetGitSummary(), gv.GitSummary)
			require.Equal(t, gitSummary{}.GetBuildDate(), gv.BuildDate)
			require.Equal(t, gitSummary{}.GetBinaryVersion(), gv.BinaryVersion)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		exporter, err := publisher.NewHTTPExporter(ts.URL, "")
		require.NoError(t, err)
		nodeID := strings.Replace(uuid.NewString(), "-", "", -1)
		p := publisher.NewPublisher(s, exporter, nodeID, time.Second)
		p.Start()

		require.Eventually(t, func() bool {
			metrics, err = s.FetchUnpublishedMetrics(context.Background(), 2)
			require.NoError(t, err)
			return len(metrics) == 0
		}, 5*time.Second, time.Second)

		p.Close()
	})
}

type gitSummary struct{}

func (gs gitSummary) GetGitCommit() string     { return "fakeGitCommit" }
func (gs gitSummary) GetGitBranch() string     { return "fakeGitBranch" }
func (gs gitSummary) GetGitState() string      { return "fakeGitState" }
func (gs gitSummary) GetGitSummary() string    { return "fakeGitSummary" }
func (gs gitSummary) GetBuildDate() string     { return "fakeGitDate" }
func (gs gitSummary) GetBinaryVersion() string { return "fakeBinaryVersion" }

type stateHash struct{}

func (h stateHash) ChainID() int64 {
	return 1
}

func (h stateHash) BlockNumber() int64 {
	return 1
}

func (h stateHash) Hash() string {
	return "abcdefgh"
}
