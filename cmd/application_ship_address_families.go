package cmd

// The address family line under ship's Live line (ankra-oznf7k.3). A public
// deployment on a load balancer with no IPv4 address looks fine while visitors
// on IPv4-only networks cannot connect, and IPv4 is priced, so it is never
// there unless someone asked for it. The hostname's DNS records are what those
// visitors get, whichever way the address got there: a load balancer's own or
// the Ankra relay's.

import (
	"context"
	"net"
	"net/url"
	"time"
)

const (
	shipAddressFamilyIPv4 = "ipv4"
	shipAddressFamilyIPv6 = "ipv6"
)

// shipAddressLookupTimeout bounds the DNS read, so a slow resolver costs a
// finished ship a moment and no more. shipLookupAddresses is a variable so
// tests stay off the network.
const shipAddressLookupTimeout = 5 * time.Second

var shipLookupAddresses = func(lookupContext context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(lookupContext, host)
}

// shipURLAddressFamilies answers the families the address's hostname resolves
// to, IPv4 first. The address may be a full URL, with any scheme, port or
// path, or a bare hostname. The lookup keeps the wait's values but not its
// deadline, which may already have passed when the wait ended. A lookup that
// fails answers none, which the caller words as "could not read", never as a
// family being absent.
func shipURLAddressFamilies(waitContext context.Context, address string) []string {
	lookupContext, cancel := context.WithTimeout(context.WithoutCancel(waitContext), shipAddressLookupTimeout)
	defer cancel()
	addresses, lookupError := shipLookupAddresses(lookupContext, shipAddressHostname(address))
	if lookupError != nil {
		return []string{}
	}
	return shipAddressFamiliesOf(addresses)
}

// shipAddressHostname is the hostname of a URL, or the address itself when it
// is already a bare hostname.
func shipAddressHostname(address string) string {
	parsed, parseError := url.Parse(address)
	if parseError != nil || parsed.Hostname() == "" {
		return address
	}
	return parsed.Hostname()
}

func shipAddressFamiliesOf(addresses []net.IPAddr) []string {
	hasIPv4, hasIPv6 := false, false
	for _, address := range addresses {
		if address.IP.To4() != nil {
			hasIPv4 = true
		} else if address.IP != nil {
			hasIPv6 = true
		}
	}
	families := []string{}
	if hasIPv4 {
		families = append(families, shipAddressFamilyIPv4)
	}
	if hasIPv6 {
		families = append(families, shipAddressFamilyIPv6)
	}
	return families
}

func shipAddressFamiliesLine(families []string) string {
	hasIPv4, hasIPv6 := false, false
	for _, family := range families {
		hasIPv4 = hasIPv4 || family == shipAddressFamilyIPv4
		hasIPv6 = hasIPv6 || family == shipAddressFamilyIPv6
	}
	switch {
	case hasIPv4 && hasIPv6:
		return "Answers on: IPv4 and IPv6"
	case hasIPv6:
		return "Answers on: IPv6 only. Visitors on IPv4-only networks cannot connect."
	case hasIPv4:
		return "Answers on: IPv4 only"
	default:
		return "Answers on: not known yet, the hostname has no DNS record Ankra could read."
	}
}
