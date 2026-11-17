package store

import "testing"

func TestMigrationsSplit(t *testing.T) {
	for _, name := range []string{"001_init.sql", "002_timescale_policies.sql"} {
		b, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(splitStatements(string(b))); n < 3 {
			t.Errorf("%s: %d statements", name, n)
		}
	}
}
