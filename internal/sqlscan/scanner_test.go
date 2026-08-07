package sqlscan

import "testing"

func TestStatements(t *testing.T) {
	t.Parallel()

	sql := `SELECT ';' AS value; -- ignored ;
SELECT $$also ; ignored$$;
CREATE FUNCTION f() RETURNS void AS $body$
BEGIN
  PERFORM 1;
END;
$body$ LANGUAGE plpgsql;`
	spans := Statements(sql)
	if len(spans) != 3 {
		t.Fatalf("Statements() count = %d, want 3: %#v", len(spans), spans)
	}
}

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

func TestAt(t *testing.T) {
	t.Parallel()

	sql := "SELECT 1;\nSELECT 2;"
	span, ok := At(sql, len("SELECT 1;\nSEL"))
	if !ok || span.Text != "SELECT 2;" {
		t.Fatalf("At() = %#v, %v; want second statement", span, ok)
	}
}

func FuzzStatements(f *testing.F) {
	f.Add("SELECT 1;")
	f.Add("SELECT ';'; -- ;\nSELECT 2")
	f.Add("DO $$ BEGIN PERFORM 1; END $$;")
	f.Fuzz(func(t *testing.T, input string) {
		spans := Statements(input)
		previousEnd := 0
		for _, span := range spans {
			if span.Start < previousEnd || span.Start < 0 || span.End > len(input) || span.Start >= span.End {
				t.Fatalf("invalid span %#v for input length %d", span, len(input))
			}
			previousEnd = span.End
		}
	})
}
