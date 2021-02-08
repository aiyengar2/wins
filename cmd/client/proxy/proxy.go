package proxy

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/rancher/wins/pkg/npipes"
	"github.com/sirupsen/logrus"
)

type proxy struct {
	listener  proxyListener
	responder proxyResponder

	proxyConn net.Conn
	cancel    context.CancelFunc
}

func initProxy(ctx context.Context, id string, npipeDialer npipes.Dialer) (p proxy, err error) {
	ctx, p.cancel = context.WithCancel(ctx)
	p.proxyConn, err = npipeDialer(ctx, "")
	if err != nil {
		return p, fmt.Errorf("failed to open connection to named pipe: %v", err)
	}
	go func() {
		select {
		case <-ctx.Done():
			p.proxyConn.Close()
		}
	}()
	// first message sent across the wire is id
	_, err = p.proxyConn.Write([]byte(id))
	if err != nil {
		return p, fmt.Errorf("failed to write to named pipe")
	}
	logrus.Infof("waiting for connection to be established with proxy server")
	p.proxyConn.SetReadDeadline(time.Now().Add(time.Minute))
	buf := make([]byte, 1)
	_, err = p.proxyConn.Read(buf)
	if err != nil {
		return p, fmt.Errorf("unable to establish connection with proxy server: %v", err)
	}
	// Unset read deadline
	p.proxyConn.SetReadDeadline(time.Time{})
	logrus.Infof("starting proxy")
	p.responder.serve(ctx, p.proxyConn)
	p.listener.serve(ctx, p.proxyConn)
	return p, nil
}

func (p *proxy) listen(ipVersion string, ipProtocol string, port uint16) (err error) {
	return p.listener.addFilter(ipVersion, ipProtocol, port)
}

func (p *proxy) close() {
	if p.cancel != nil {
		p.cancel()
	}
}
