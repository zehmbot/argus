package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "config error",
			err:  fmt.Errorf("%w: storage.local is required", errConfig),
			want: exitConfig,
		},
		{
			name: "config error wrapped further",
			err:  fmt.Errorf("running backup: %w", fmt.Errorf("%w: bad dsn", errConfig)),
			want: exitConfig,
		},
		{
			name: "storage error",
			err:  fmt.Errorf("%w: uploading artifact: connection reset", errStorage),
			want: exitStorage,
		},
		{
			name: "anything else is a failed backup",
			err:  errors.New("pg_dump: exit status 1"),
			want: exitBackup,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeFor(tc.err); got != tc.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
