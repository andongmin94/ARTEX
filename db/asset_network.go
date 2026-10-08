package db

import (
	"fmt"
	"net/netip"
)

// SQLite compares fixed-width, big-endian address BLOBs within one family.
func sqliteAddress(raw string) (normalized string, family int, address []byte, err error) {
	if raw == "" {
		return "", 0, nil, nil
	}
	if err := ValidateAssetIP(raw); err != nil {
		return "", 0, nil, err
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil || ip.Zone() != "" {
		return "", 0, nil, fmt.Errorf("%w: %s", ErrAssetIPInvalid, raw)
	}
	ip = ip.Unmap()
	family = 6
	if ip.Is4() {
		family = 4
	}
	return ip.String(), family, ip.AsSlice(), nil
}

func sqliteNetwork(raw string) (normalized string, family, prefix int, first, last []byte, err error) {
	if raw == "" {
		return "", 0, 0, nil, nil, nil
	}
	net, err := netip.ParsePrefix(raw)
	if err != nil {
		return "", 0, 0, nil, nil, fmt.Errorf("유효하지 않은 CIDR: %s", raw)
	}
	if net.Addr().Is4In6() {
		if net.Bits() < 96 {
			return "", 0, 0, nil, nil, fmt.Errorf("유효하지 않은 IPv4 CIDR: %s", raw)
		}
		net = netip.PrefixFrom(net.Addr().Unmap(), net.Bits()-96)
	}
	net = net.Masked()
	family = 6
	if net.Addr().Is4() {
		family = 4
	}
	first = net.Addr().AsSlice()
	last = append([]byte(nil), first...)
	prefix = net.Bits()
	for bit := prefix; bit < len(last)*8; bit++ {
		last[bit/8] |= 1 << (7 - uint(bit%8))
	}
	return net.String(), family, prefix, first, last, nil
}
