package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/tracker"
)

func TestSmokeTestWebServer(t *testing.T) {
	trk := tracker.New(-31.94, 115.97)
	srv := New(trk, &mockStats{}, nil, nil, nil, nil, nil)
	if err := srv.Start(0); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Stats endpoint
	resp, err := http.Get("http://" + srv.Addr() + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stats: status %d", resp.StatusCode)
	}
	var stats StatsData
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("stats decode: %v", err)
	}

	respDrops, err := http.Get("http://" + srv.Addr() + "/api/drops")
	if err != nil {
		t.Fatal(err)
	}
	defer respDrops.Body.Close()
	if respDrops.StatusCode != 200 {
		t.Fatalf("drops: status %d", respDrops.StatusCode)
	}

	// Aircraft endpoint
	resp2, err := http.Get("http://" + srv.Addr() + "/api/aircraft")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("aircraft: status %d", resp2.StatusCode)
	}
	var aircraft []tracker.Aircraft
	if err := json.NewDecoder(resp2.Body).Decode(&aircraft); err != nil {
		t.Fatalf("aircraft decode: %v", err)
	}

	// Dashboard HTML
	resp3, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("dashboard: status %d", resp3.StatusCode)
	}
}
