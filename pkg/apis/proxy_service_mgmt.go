package apis

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/rancher/wins/pkg/defaults"
	"github.com/rancher/wins/pkg/npipes"
	"github.com/sirupsen/logrus"
)

type proxyService struct {
	cancel  context.CancelFunc
	proxies []proxy
}

func initProxyService(ctx context.Context) (s *proxyService, err error) {
	ctx, s.cancel = context.WithCancel(ctx)
	proxyPath := npipes.GetFullPath(defaults.ProxyPipeName)
	proxyListener, err := npipes.New(proxyPath, "", 0)
	if err != nil {
		return s, fmt.Errorf("could not create and listen to named pipe: %s", err)
	}
	go func() {
		select {
		case <-ctx.Done():
			proxyListener.Close()
		}
	}()
	go s.serve(ctx, proxyListener)
	return s, err
}

func (s *proxyService) serve(ctx context.Context, listener net.Listener) {
	defer s.close()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Accept new connections
		proxyConn, err := listener.Accept()
		if err == io.EOF {
			// Connection was closed
			return
		}
		if err != nil {
			logrus.Errorf("unable to accept a new connection to the named pipe: %v", err)
			continue
		}
		go func() {
			if _, err := initProxy(ctx, proxyConn); err != nil {
				logrus.Errorf("unable to start proxy to handle connection: %v", err)
			}
		}()
	}
}

func (s *proxyService) close() {
	if s.cancel != nil {
		s.cancel()
	}
}

type proxy struct {
	receiver proxyReceiver
	server   proxyServer

	proxyConn net.Conn
	cancel    context.CancelFunc
}

func initProxy(ctx context.Context, proxyConn net.Conn) (p proxy, err error) {
	go func() {
		select {
		case <-ctx.Done():
			p.proxyConn.Close()
		}
	}()
	id := make([]byte, 256)
	_, err = proxyConn.Read(id)
	if err != nil {
		return p, fmt.Errorf("failed to read proxy identifier sent through named pipe")
	}
	// Get ports to dial
	dialerConfigs := _register.getDialerConfigs(string(id))
	for d := range dialerConfigs {
		p.server.addDialer(d)
	}
	p.receiver.serve(ctx, p.proxyConn)
	p.server.serve(ctx, p.proxyConn)
	return p, nil
}

// type proxyRegister struct {
// 	mu sync.RWMutex

// 	dialerConfigs map[string]chan proxyDialerConfig
// }

// var _register proxyRegister

// func (r *proxyRegister) register(req types.ProxyKeepAliveRequest) error {
// 	r.mu.Lock()
// 	defer r.mu.Unlock()
// 	dialerConfigs := r.dialerConfigs[req.ID]
// 	if dialerConfigs == nil {
// 		dialerConfigs = make(chan proxyDialerConfig, len(req.Ports)*2)
// 		// Leave behind channel so a connection can claim it
// 		r.dialerConfigs[req.ID] = dialerConfigs
// 	} else {
// 		// Connection has already claimed this channel
// 		delete(r.dialerConfigs, req.ID)
// 	}
// 	for _, p := range req.Ports {
// 		address := fmt.Sprintf(":%d", p.Port)
// 		if p.Protocol == types.Protocol_TCP {
// 			dialerConfigs <- proxyDialerConfig{
// 				network: "ip4:tcp",
// 				address: address,
// 			}
// 			dialerConfigs <- proxyDialerConfig{
// 				network: "ip6:tcp",
// 				address: address,
// 			}
// 		} else {
// 			dialerConfigs <- proxyDialerConfig{
// 				network: "ip4:udp",
// 				address: address,
// 			}
// 			dialerConfigs <- proxyDialerConfig{
// 				network: "ip6:udp",
// 				address: address,
// 			}
// 		}
// 	}
// 	close(dialerConfigs)
// 	return nil
// }

// func (r *proxyRegister) getDialerConfigs(id string) (dialerConfigs chan proxyDialerConfig) {
// 	r.mu.Lock()
// 	defer r.mu.Unlock()
// 	dialerConfigs = r.dialerConfigs[id]
// 	if dialerConfigs == nil {
// 		dialerConfigs = make(chan proxyDialerConfig, 10)
// 		// Leave behind a channel that a register could send dialerConfigs through
// 		r.dialerConfigs[id] = dialerConfigs
// 	} else {
// 		// Claiming this channel for this connection
// 		delete(r.dialerConfigs, id)
// 	}
// 	return dialerConfigs
// }

type proxyReceiver struct {
}

func (r *proxyReceiver) serve(ctx context.Context, proxyConn net.Conn) {
	go r.listen(ctx, proxyConn)
}

func (r *proxyReceiver) listen(ctx context.Context, in net.Conn) {

}

type proxyServer struct {
}

type proxyDialerConfig struct {
	network string
	address string
}

func (s *proxyServer) serve(ctx context.Context, proxyConn net.Conn) {
	go s.listen(ctx, proxyConn)
}

func (s *proxyServer) listen(ctx context.Context, in net.Conn) {

}

func (s *proxyServer) addDialer(d proxyDialerConfig) (err error) {

}

// type proxyServer struct {
// 	proxyListener *net.Listener

// 	localNetworkConn *net.PacketConn
// }

// func (s *proxyServer) createPipe(ctx context.Context) (err error) {
// 	if s.proxyListener == nil {
// 		proxyPath := npipes.GetFullPath(defaults.ProxyPipeName)
// 		proxyListener, err := npipes.New(proxyPath, "", 0)
// 		if err != nil {
// 			return errors.Wrapf(err, "could not listen %s", proxyPath)
// 		}
// 		s.proxyListener = &proxyListener
// 	}
// 	return nil
// }

// func (s *proxyServer) connectToHostNetwork() (err error) {
// 	if s.localNetworkConn == nil {
// 		localNetworkConn, err := net.ListenPacket(l.network, l.address)
// 		if err != nil {
// 			return fmt.Errorf("Failed to listen to %s packets at %s: %v", l.network, l.address, err)
// 		}
// 		s.localNetworkConn = &localNetworkConn
// 	}
// 	return nil
// }

type portTracker struct {
	addport    chan uint16
	removeport chan uint16
	checkport  chan uint16
	isopen     chan bool
}

func initPortTracker(ctx context.Context) (t *portTracker) {
	addport := make(chan uint16, 1)
	removeport := make(chan uint16, 1)
	checkport := make(chan uint16, 1)
	isopen := make(chan bool, 1)
	go func() {
		var ports map[uint16]uint32
		for {
			select {
			case <-ctx.Done():
				return
			case port := <-addport:
				ports[port]++
			case port := <-removeport:
				ports[port]--
			case port := <-checkport:
				numHolds, ok := ports[port]
				if !ok {
					isopen <- false
					continue
				}
				if numHolds <= 1 {
					delete(ports, port)
					isopen <- false
					continue
				}
				ports[port]--
				isopen <- true
			}
		}
	}()
	return &portTracker{
		addport:    addport,
		removeport: removeport,
		checkport:  checkport,
		isopen:     isopen,
	}
}

func (p *portTracker) register(port uint16) {
	p.addport <- port
}

func (p *portTracker) unregister(port uint16) {
	p.removeport <- port
}

func (p *portTracker) isRegistered(port uint16) bool {
	p.checkport <- port
	return <-p.isopen
}
