package directory

// Which address this controller serves, and which interface owns it.
//
// `samba-tool domain provision` chooses one of this host's IPv4 addresses when
// it is not told which, and says so rather than asking:
//
//	WARNING … provision/__init__.py #2122: More than one IPv4 address found.
//	Using 192.168.122.1
//
// On any host running docker or libvirt that is a coin toss between a bridge
// and the address the machine is actually reached on, and whichever it lands on
// goes into the DC's own A record — the record every member of the domain
// resolves. So the wizard asks, and the answer decides a second thing too: the
// DC binds the interface that owns it plus loopback, instead of trying every
// address on the host and failing where another dnsmasq already holds port 53.
//
// Nothing here starts a process, and nothing here runs `ip` or `ifconfig`.
// net.Interfaces reads the kernel's own interface table, and the address the
// kernel would send from is learned by opening an unconnected UDP socket
// toward a documentation address: a connect on a datagram socket sends no
// packet, it only asks the routing table which local address it would use.

import (
	"net"
	"strings"
)

// HostAddress is one IPv4 address this host has, with the interface that owns
// it. The interface travels with the address because on a host with four of
// them the interface name is what tells them apart, and because it is what the
// provision's `interfaces` option needs.
type HostAddress struct {
	IP    string
	Iface string
}

// Label renders the address the way the wizard's picker lists it.
func (h HostAddress) Label() string {
	if h.Iface == "" {
		return h.IP
	}
	return h.IP + " on " + h.Iface
}

// Interface is one network interface as the address picker needs to see it.
// It exists so the filtering and the ordering below can be tested against a
// fixed set of interfaces rather than against whatever machine runs the test.
type Interface struct {
	Name string
	// Up and Loopback are the two flags that decide whether an interface can
	// carry the address a controller serves on.
	Up       bool
	Loopback bool
	// Addrs are the addresses on the interface, in the CIDR form
	// net.Interface.Addrs returns ("192.168.10.20/24"); a bare address is
	// accepted too.
	Addrs []string
}

// SelectHostAddresses returns the IPv4 addresses a domain controller could
// serve on, preferred one first.
//
// What is left out is as deliberate as what is kept. A down interface has no
// address to serve from; loopback is where the DC listens anyway and is never
// the address a member resolves; a link-local address (169.254/16) means DHCP
// did not answer, and a domain built on one would break the moment it does.
// IPv6 is left out as a whole: this is the value of `--host-ip`, and
// `--host-ip6` is a separate answer this wizard does not collect.
func SelectHostAddresses(ifaces []Interface, preferred string) []HostAddress {
	var addrs []HostAddress
	for _, iface := range ifaces {
		if !iface.Up || iface.Loopback {
			continue
		}
		for _, raw := range iface.Addrs {
			if ip := serviceableIPv4(raw); ip != "" {
				addrs = append(addrs, HostAddress{IP: ip, Iface: iface.Name})
			}
		}
	}
	return preferredFirst(addrs, preferred)
}

// preferredFirst moves the address on the default route to the front, so the
// picker opens on the answer that is right on most hosts without taking the
// choice away.
func preferredFirst(addrs []HostAddress, preferred string) []HostAddress {
	if preferred == "" {
		return addrs
	}
	for i, addr := range addrs {
		if addr.IP != preferred || i == 0 {
			continue
		}
		ordered := make([]HostAddress, 0, len(addrs))
		ordered = append(ordered, addr)
		ordered = append(ordered, addrs[:i]...)
		ordered = append(ordered, addrs[i+1:]...)
		return ordered
	}
	return addrs
}

// serviceableIPv4 returns the plain IPv4 address of one entry, or empty for an
// entry a controller cannot serve from.
func serviceableIPv4(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	ip, _, err := net.ParseCIDR(raw)
	if err != nil {
		ip = net.ParseIP(raw)
	}
	if ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return ""
	}
	return v4.String()
}

// HostAddresses reads this host's interfaces and returns what
// SelectHostAddresses makes of them.
func HostAddresses() []HostAddress {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	list := make([]Interface, 0, len(ifaces))
	for _, iface := range ifaces {
		entry := Interface{
			Name:     iface.Name,
			Up:       iface.Flags&net.FlagUp != 0,
			Loopback: iface.Flags&net.FlagLoopback != 0,
		}
		addrs, err := iface.Addrs()
		if err != nil {
			// An interface whose addresses cannot be read is not an error worth
			// failing the whole list for: the others are still true.
			continue
		}
		for _, addr := range addrs {
			entry.Addrs = append(entry.Addrs, addr.String())
		}
		list = append(list, entry)
	}
	return SelectHostAddresses(list, PreferredHostIP())
}

// preferredProbe is where the source-address probe points: 192.0.2.1 is in the
// documentation range reserved by RFC 5737, so it is an address that can never
// be a real host, and port 9 is discard. Nothing is sent — see PreferredHostIP.
const preferredProbe = "192.0.2.1:9"

// PreferredHostIP is the local address the kernel would send from, which on a
// host with several addresses is the one on the default route.
//
// net.Dial on udp does not send a packet: it resolves the route and binds a
// local address, which is exactly the question being asked. An unroutable host
// (no default route at all) answers with an error, and an empty string is the
// honest answer to "which one would be used" there.
func PreferredHostIP() string {
	conn, err := net.Dial("udp", preferredProbe)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	host, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return ""
	}
	return serviceableIPv4(host)
}

// FindHostAddress returns the address whose label a picker reported selected.
// The picker deals in strings, and this is the one place they are turned back
// into the address and the interface that produced them.
func FindHostAddress(addrs []HostAddress, label string) (HostAddress, bool) {
	for _, addr := range addrs {
		if addr.Label() == label {
			return addr, true
		}
	}
	return HostAddress{}, false
}

// HostAddressLabels renders the list for the picker, in order.
func HostAddressLabels(addrs []HostAddress) []string {
	labels := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		labels = append(labels, addr.Label())
	}
	return labels
}
