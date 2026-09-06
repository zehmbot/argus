package postgres

import "testing"

func TestFingerprintColumns_Deterministic(t *testing.T) {
	columns := []columnInfo{
		{Table: "orders", Column: "id", Type: "integer"},
		{Table: "orders", Column: "total", Type: "numeric"},
		{Table: "users", Column: "id", Type: "integer"},
	}

	first := fingerprintColumns(columns)
	second := fingerprintColumns(columns)

	if first != second {
		t.Errorf("fingerprintColumns() is not deterministic: %s != %s", first, second)
	}
}

func TestFingerprintColumns_DetectsSchemaChange(t *testing.T) {
	before := []columnInfo{
		{Table: "users", Column: "id", Type: "integer"},
		{Table: "users", Column: "name", Type: "text"},
	}

	cases := []struct {
		name  string
		after []columnInfo
	}{
		{
			name: "column added",
			after: []columnInfo{
				{Table: "users", Column: "id", Type: "integer"},
				{Table: "users", Column: "name", Type: "text"},
				{Table: "users", Column: "email", Type: "text"},
			},
		},
		{
			name: "column removed",
			after: []columnInfo{
				{Table: "users", Column: "id", Type: "integer"},
			},
		},
		{
			name: "type changed",
			after: []columnInfo{
				{Table: "users", Column: "id", Type: "bigint"},
				{Table: "users", Column: "name", Type: "text"},
			},
		},
	}

	beforeHash := fingerprintColumns(before)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			afterHash := fingerprintColumns(tc.after)
			if afterHash == beforeHash {
				t.Errorf("fingerprintColumns() did not change for %s", tc.name)
			}
		})
	}
}

func TestFingerprintColumns_Format(t *testing.T) {
	got := fingerprintColumns([]columnInfo{{Table: "t", Column: "c", Type: "int"}})

	const prefix = "sha256:"
	if len(got) != len(prefix)+64 || got[:len(prefix)] != prefix {
		t.Errorf("fingerprintColumns() = %q, want %q<64 hex chars>", got, prefix)
	}
}
