package main

import (
	"reflect"
	"testing"
)

func TestParseScutilNameservers(t *testing.T) {
	out := []byte(`DNS configuration

resolver #1
  search domain[0] : home.example
  nameserver[0] : 192.0.2.1
  nameserver[1] : 192.0.2.1
  nameserver[2] : 192.0.2.2
  nameserver[3] : not-an-ip
  if_index : 14 (en0)
  flags    : Request A records
  reach    : 0x00020002 (Reachable,Directly Reachable Address)

resolver #2
  domain   : local
  nameserver[0] : 10.0.0.53
  options  : mdns

resolver #3
  domain   : 254.169.in-addr.arpa
  options  : mdns
`)
	want := []string{"192.0.2.1", "192.0.2.2"}
	got := parseScutilNameservers(out)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseScutilNameservers = %v, want %v (only the primary block, deduped, valid IPs)", got, want)
	}
}

func TestParseScutilNameserversEmptyPrimary(t *testing.T) {
	if got := parseScutilNameservers([]byte("DNS configuration\n\nresolver #1\n  flags : Request A records\n")); len(got) != 0 {
		t.Errorf("expected no nameservers, got %v", got)
	}
}

func TestParseResolvConfNameservers(t *testing.T) {
	data := []byte("# comment\nnameserver 192.0.2.1\nnameserver fe80::1%en0\nsearch home.lan\nnameserver bogus\n")
	want := []string{"192.0.2.1", "fe80::1%en0"}
	got := parseResolvConfNameservers(data)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseResolvConfNameservers = %v, want %v", got, want)
	}
}
