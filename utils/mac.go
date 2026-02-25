package utils

import (
	"crypto/rand"
	"fmt"
	"net"
)

// GenerateMAC generates a random locally-administered unicast MAC address.
// The first byte has bit 1 set (locally administered) and bit 0 clear (unicast).
func GenerateMAC() (net.HardwareAddr, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("generate MAC: %w", err)
	}
	buf[0] = (buf[0] | 0x02) & 0xFE // locally administered, unicast
	return net.HardwareAddr(buf[:]), nil
}
