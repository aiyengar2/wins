package proxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"

	"github.com/sirupsen/logrus"
)

type proxyResponder struct {
}

func (r *proxyResponder) serve(ctx context.Context, proxyConn net.Conn) {
	go r.listen(ctx, proxyConn)
}

func (r *proxyResponder) listen(ctx context.Context, in net.Conn) {
	// Should be big enough for ip4 or ip6
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		buf := make([]byte, 1+2+16+65535)
		n, err := in.Read(buf)
		if err != nil {
			if err == io.EOF {
				logrus.Errorf("[%s] connection closed", in.RemoteAddr())
				return
			}
			logrus.Errorf("[%s] error while trying to read data from named pipe: %v", in.RemoteAddr(), err)
		}
		if n < 1+2 {
			logrus.Errorf("[%s] received invalid response from pipe: less than 3 bytes", in.RemoteAddr())
			continue
		}
		// Parse data to dial
		var network string
		var address string
		var data []byte
		switch buf[0] {
		case 0x00:
			address = string(buf[3:7])
			data = buf[7:n]
			switch binary.BigEndian.Uint16(buf[1:3]) {
			case 11:
				network += "ip4:tcp"
			case 6:
				network += "ip4:udp"
			default:
				logrus.Errorf("[%s] received invalid response from pipe: invalid ipProtocol %v", in.RemoteAddr(), buf[1:3])
				continue
			}
		case 0x01:
			address = string(buf[3:19])
			data = buf[19:n]
			switch binary.BigEndian.Uint16(buf[1:3]) {
			case 11:
				network += "ip6:tcp"
			case 6:
				network += "ip6:udp"
			default:
				logrus.Errorf("[%s] received invalid response from pipe: invalid ipProtocol %v", in.RemoteAddr(), buf[1:3])
				continue
			}
		default:
			logrus.Errorf("[%s] received invalid response from pipe: invalid ipVersion %v", in.RemoteAddr(), buf[0])
			continue
		}
		raddr, err := net.ResolveIPAddr(network, address)
		if err != nil {
			logrus.Errorf("[%s] received invalid response from pipe: unable to resolve %s to address on network %s: %v", in.RemoteAddr(), address, network, err)
			continue
		}
		out, err := net.DialIP(network, nil, raddr)
		if err != nil {
			logrus.Errorf("[%s] unable to dial address %s on network %s: %v", in.RemoteAddr(), address, network, err)
			continue
		}
		if _, err := out.WriteToIP(data, raddr); err != nil {
			logrus.Errorf("%s] unable to write to address %s on network %s: %v", in.RemoteAddr(), address, network, err)
			continue
		}
	}
}
