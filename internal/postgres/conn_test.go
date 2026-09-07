package postgres

import (
	"strings"
	"testing"
)

func TestParseDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want ConnInfo
	}{
		{
			name: "url form",
			dsn:  "postgres://argus:argus@db.internal:5432/app_production?sslmode=disable",
			want: ConnInfo{Host: "db.internal", Database: "app_production"},
		},
		{
			name: "keyword value form",
			dsn:  "host=db.internal port=5432 dbname=app_production user=argus",
			want: ConnInfo{Host: "db.internal", Database: "app_production"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDSN(tc.dsn)
			if err != nil {
				t.Fatalf("ParseDSN() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseDSN() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseDSN_Invalid(t *testing.T) {
	if _, err := ParseDSN("postgres://db.internal:not-a-port/app"); err == nil {
		t.Error("ParseDSN() error = nil, want error")
	}
}

// The DSN carries credentials and its parse error is the one place they
// could reach a log line, so pin that they do not.
func TestParseDSN_ErrorOmitsPassword(t *testing.T) {
	const password = "hunter2"

	_, err := ParseDSN("postgres://argus:" + password + "@db.internal:not-a-port/app")
	if err == nil {
		t.Fatal("ParseDSN() error = nil, want error")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("ParseDSN() error = %v, want the password redacted", err)
	}
}
