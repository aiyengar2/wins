package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/sirupsen/logrus"
)

type proxyListener struct {
	ip4UDPFilters map[uint16]bool
	ip4TCPFilters map[uint16]bool
	ip6UDPFilters map[uint16]bool
	ip6TCPFilters map[uint16]bool
}

func (l *proxyListener) serve(ctx context.Context, proxyConn net.Conn) {
	for _, ipNetwork := range []string{"ip4:tcp", "ip4:udp", "ip6:tcp", "ip6:udp"} {
		go l.listen(ctx, ipNetwork, proxyConn)
	}
}

func (l *proxyListener) listen(ctx context.Context, ipNetwork string, out net.Conn) {
	in, err := net.ListenIP(ipNetwork, nil)
	if err != nil {
		logrus.Errorf("Failed to listen to %s: %v", ipNetwork, err)
		return
	}
	defer in.Close()
	var filter map[uint16]bool
	var buf []byte
	var sourceIPBuf []byte
	var dataBuf []byte
	switch ipNetwork {
	case "ip4:tcp":
		filter = l.ip4TCPFilters
		buf = make([]byte, 1+2+4+4+65535)
		// protocol number is 6 (0x06)
		copy(buf[0:3], []byte{0x00, 0x00, 0x06})
		sourceIPBuf = buf[3:7]
		dataBuf = buf[7:]
	case "ip4:udp":
		filter = l.ip4UDPFilters
		buf = make([]byte, 1+2+4+65535)
		// protocol number is 17 (0x11)
		copy(buf[0:3], []byte{0x00, 0x00, 0x11})
		sourceIPBuf = buf[3:7]
		dataBuf = buf[7:]
	case "ip6:tcp":
		filter = l.ip6TCPFilters
		buf = make([]byte, 1+2+16+65535)
		// protocol number is 6 (0x06)
		copy(buf[0:3], []byte{0x01, 0x00, 0x06})
		sourceIPBuf = buf[3:19]
		dataBuf = buf[19:]
	case "ip6:udp":
		filter = l.ip6UDPFilters
		buf = make([]byte, 1+2+16+65535)
		// protocol number is 17 (0x11)
		copy(buf[0:3], []byte{0x01, 0x00, 0x11})
		sourceIPBuf = buf[3:19]
		dataBuf = buf[19:]
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Get IP Packet data
		n, returnAddr, err := in.ReadFromIP(dataBuf)
		if err != nil {
			if err == io.EOF {
				logrus.Errorf("[%s] connection closed", ipNetwork)
				return
			}
			logrus.Errorf("[%s] error while trying to read IP packets from local network: %v", ipNetwork, err)
		}
		// Parse destination port and check if it is valid
		if n < 4 {
			logrus.Errorf("[%s] received invalid packet: less than 4 bytes", ipNetwork)
			continue
		}
		destPort := binary.BigEndian.Uint16(buf[2:4])
		if val, ok := filter[destPort]; !ok || !val {
			// not listening to port
			continue
		}
		// Copy source IP
		if len(sourceIPBuf) != len(returnAddr.IP) {
			logrus.Errorf("[%s] received invalid packet: unable to parse source ip %v", ipNetwork, returnAddr.IP)
			continue
		}
		copy(sourceIPBuf, returnAddr.IP)
		// Write to named pipe
		if _, err := out.Write(buf); err != nil {
			logrus.Errorf("[%s] could not write packet: %v", out.RemoteAddr(), err)
			continue
		}
	}
}

func (l *proxyListener) addFilter(ipVersion string, ipProtocol string, port uint16) error {
	switch ipVersion {
	case "ip4":
		switch ipProtocol {
		case "tcp":
			l.ip4TCPFilters[port] = true
		case "udp":
			l.ip4UDPFilters[port] = true
		default:
			return fmt.Errorf("received invalid ipNetwork %s:%s", ipVersion, ipProtocol)
		}
	case "ip6":
		switch ipProtocol {
		case "tcp":
			l.ip6TCPFilters[port] = true
		case "udp":
			l.ip6UDPFilters[port] = true
		default:
			return fmt.Errorf("received invalid ipNetwork %s:%s", ipVersion, ipProtocol)
		}
	default:
		return fmt.Errorf("received invalid ipVersion %s", ipVersion)
	}
	return nil
}
