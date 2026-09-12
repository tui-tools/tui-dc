package directory

import "testing"

// labHost is the host the address question exists for: the address it is
// actually reached on, a docker bridge, a libvirt bridge, one more bridge, a
// down interface and loopback.
func labHost() []Interface {
	return []Interface{
		{Name: "lo", Up: true, Loopback: true, Addrs: []string{"127.0.0.1/8", "::1/128"}},
		{Name: "eth0", Up: true, Addrs: []string{
			"192.168.10.20/24", "fe80::1234:5678:9abc:def0/64"}},
		{Name: "docker0", Up: true, Addrs: []string{"172.17.0.1/16"}},
		{Name: "virbr0", Up: true, Addrs: []string{"192.168.122.1/24"}},
		{Name: "eth1", Up: false, Addrs: []string{"192.168.11.20/24"}},
		{Name: "wlan0", Up: true, Addrs: []string{"169.254.7.7/16"}},
	}
}

// TestSelectHostAddressesOrdersAndFilters: what the picker offers, and what it
// refuses to offer. Loopback, a down interface, a link-local address and IPv6
// are all left out, and the address on the default route comes first.
func TestSelectHostAddressesOrdersAndFilters(t *testing.T) {
	addrs := SelectHostAddresses(labHost(), "192.168.122.1")
	want := []HostAddress{
		{IP: "192.168.122.1", Iface: "virbr0"},
		{IP: "192.168.10.20", Iface: "eth0"},
		{IP: "172.17.0.1", Iface: "docker0"},
	}
	if len(addrs) != len(want) {
		t.Fatalf("addresses = %+v", addrs)
	}
	for i, addr := range want {
		if addrs[i] != addr {
			t.Errorf("address %d = %+v, want %+v", i, addrs[i], addr)
		}
	}
}

// TestSelectHostAddressesKeepsKernelOrderWithoutAPreference: with no default
// route to learn from, the list is the kernel's own order rather than a guess.
func TestSelectHostAddressesKeepsKernelOrderWithoutAPreference(t *testing.T) {
	addrs := SelectHostAddresses(labHost(), "")
	if len(addrs) != 3 || addrs[0].IP != "192.168.10.20" {
		t.Fatalf("addresses = %+v", addrs)
	}
	// A preferred address that is not on the host changes nothing either.
	if same := SelectHostAddresses(labHost(), "10.0.0.1"); same[0].IP != "192.168.10.20" {
		t.Errorf("an address that is not here was preferred: %+v", same)
	}
}

// TestSelectHostAddressesOnASingleAddressHost: one address, which is the case
// the wizard skips the question for.
func TestSelectHostAddressesOnASingleAddressHost(t *testing.T) {
	addrs := SelectHostAddresses([]Interface{
		{Name: "lo", Up: true, Loopback: true, Addrs: []string{"127.0.0.1/8"}},
		{Name: "eth0", Up: true, Addrs: []string{"192.168.10.20/24"}},
	}, "192.168.10.20")
	if len(addrs) != 1 || addrs[0].Iface != "eth0" {
		t.Fatalf("addresses = %+v", addrs)
	}
	if addrs[0].Label() != "192.168.10.20 on eth0" {
		t.Errorf("label = %q", addrs[0].Label())
	}
}

// TestSelectHostAddressesOnAHostWithNone: nothing to offer is an answer, not a
// crash — the wizard then leaves --host-ip out, as it always did.
func TestSelectHostAddressesOnAHostWithNone(t *testing.T) {
	if addrs := SelectHostAddresses([]Interface{
		{Name: "lo", Up: true, Loopback: true, Addrs: []string{"127.0.0.1/8"}},
		{Name: "eth0", Up: false, Addrs: []string{"192.168.10.20/24"}},
	}, ""); len(addrs) != 0 {
		t.Errorf("addresses = %+v", addrs)
	}
	if addrs := SelectHostAddresses(nil, ""); addrs != nil {
		t.Errorf("addresses = %+v", addrs)
	}
}

// TestFindHostAddressRoundTrip: the picker deals in labels, and a label has to
// come back as the address and the interface that produced it.
func TestFindHostAddressRoundTrip(t *testing.T) {
	addrs := SelectHostAddresses(labHost(), "192.168.10.20")
	labels := HostAddressLabels(addrs)
	if len(labels) != len(addrs) {
		t.Fatalf("labels = %q", labels)
	}
	for i, label := range labels {
		found, ok := FindHostAddress(addrs, label)
		if !ok || found != addrs[i] {
			t.Errorf("%q came back as %+v (ok=%v)", label, found, ok)
		}
	}
	if _, ok := FindHostAddress(addrs, "10.0.0.1 on eth9"); ok {
		t.Error("a label that was never offered matched an address")
	}
}

// TestServiceableIPv4 holds the filter itself, including the forms
// net.Interface.Addrs returns.
func TestServiceableIPv4(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"192.168.10.20/24", "192.168.10.20"},
		{"192.168.10.20", "192.168.10.20"},
		{" 10.1.2.3/8 ", "10.1.2.3"},
		{"127.0.0.1/8", ""},
		{"169.254.1.1/16", ""},
		{"0.0.0.0", ""},
		{"224.0.0.1", ""},
		{"fe80::1/64", ""},
		{"2001:db8::1/32", ""},
		{"::1/128", ""},
		{"", ""},
		{"not an address", ""},
	} {
		if got := serviceableIPv4(tc.in); got != tc.want {
			t.Errorf("serviceableIPv4(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
