package sqlscan

import "testing"

func TestAnalyze(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sql         string
		wantRisky   bool
		wantResults bool
	}{
		{name: "select", sql: "SELECT * FROM users", wantResults: true},
		{name: "select into", sql: "SELECT * INTO archive FROM users", wantRisky: true, wantResults: true},
		{name: "cte read", sql: "WITH items AS (SELECT 1) SELECT * FROM items", wantResults: true},
		{name: "cte mutation", sql: "WITH changed AS (DELETE FROM users RETURNING *) SELECT * FROM changed", wantRisky: true, wantResults: true},
		{name: "update", sql: "UPDATE users SET active = false", wantRisky: true},
		{name: "returning", sql: "INSERT INTO users(name) VALUES ('a') RETURNING id", wantRisky: true, wantResults: true},
		{name: "pragma", sql: "PRAGMA table_info('users')", wantRisky: true, wantResults: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			analysis := Analyze(test.sql)
			if analysis.IsRisky != test.wantRisky || analysis.ReturnsRows != test.wantResults {
				t.Fatalf("Analyze() = %#v, want risky=%v results=%v", analysis, test.wantRisky, test.wantResults)
			}
		})
	}
}
