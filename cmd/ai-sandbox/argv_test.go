package main

import (
	"reflect"
	"testing"
)

func TestReorderFlagsFirst(t *testing.T) {
	valued := map[string]bool{"--timeout": true, "--callback-port": true}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"positional only", []string{"notion"}, []string{"notion"}},
		{"flags first", []string{"--timeout", "2s", "notion"}, []string{"--timeout", "2s", "notion"}},
		{"positional then flag", []string{"notion", "--timeout", "2s"}, []string{"--timeout", "2s", "notion"}},
		{"flag=value after positional", []string{"notion", "--timeout=2s"}, []string{"--timeout=2s", "notion"}},
		{"double-dash preserves rest as positional (and keeps the separator)", []string{"--timeout", "2s", "--", "-weird"}, []string{"--timeout", "2s", "--", "-weird"}},
		{"bool flag then positional", []string{"--help", "notion"}, []string{"--help", "notion"}},
		{"positional then bool flag", []string{"notion", "--help"}, []string{"--help", "notion"}},
		{"multi flags mixed", []string{"notion", "--timeout", "2s", "--callback-port", "49152"}, []string{"--timeout", "2s", "--callback-port", "49152", "notion"}},
		{"lone dash is positional", []string{"-", "--timeout", "2s"}, []string{"--timeout", "2s", "-"}},
	}
	for _, c := range cases {
		got := reorderFlagsFirst(c.in, valued)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
