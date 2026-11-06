package cmd

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
)

func init() {
	shipLookupAddresses = func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("no network in tests")
	}
}

func withShipAddresses(t *testing.T, addresses ...string) {
	t.Helper()
	previous := shipLookupAddresses
	t.Cleanup(func() { shipLookupAddresses = previous })
	shipLookupAddresses = func(context.Context, string) ([]net.IPAddr, error) {
		resolved := make([]net.IPAddr, 0, len(addresses))
		for _, address := range addresses {
			resolved = append(resolved, net.IPAddr{IP: net.ParseIP(address)})
		}
		return resolved, nil
	}
}

func TestShipURLAddressFamilies(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		addresses []string
		want      []string
		wantLine  string
	}{
		{"both", []string{"2001:db8::1", "192.0.2.1"}, []string{"ipv4", "ipv6"}, "Answers on: IPv4 and IPv6"},
		{"ipv6 only warns", []string{"2001:db8::1"}, []string{"ipv6"}, "IPv4-only networks cannot connect"},
		{"ipv4 only", []string{"192.0.2.1"}, []string{"ipv4"}, "Answers on: IPv4 only"},
		{"no records is not a family missing", nil, []string{}, "not known yet"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			withShipAddresses(t, testCase.addresses...)
			families := shipURLAddressFamilies(context.Background(), "shop.acme.ankra.cc")
			if !slices.Equal(families, testCase.want) {
				t.Fatalf("families = %v, want %v", families, testCase.want)
			}
			if line := shipAddressFamiliesLine(families); !strings.Contains(line, testCase.wantLine) {
				t.Fatalf("line = %q, want it to contain %q", line, testCase.wantLine)
			}
		})
	}
}

func TestShipURLAddressFamiliesReadsAFailedLookupAsUnknown(t *testing.T) {
	families := shipURLAddressFamilies(context.Background(), "shop.acme.ankra.cc")
	if len(families) != 0 {
		t.Fatalf("a failed lookup must name no family, got %v", families)
	}
}
