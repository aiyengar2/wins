package apis

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/wins/pkg/defaults"
	"github.com/rancher/wins/pkg/npipes"
	"github.com/rancher/wins/pkg/panics"
	"github.com/rancher/wins/pkg/types"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func initProxyService(ctx context.Context) (s *proxyService, err error) {
	// Create named pipe for proxy
	proxyPath := npipes.GetFullPath(defaults.ProxyPipeName)
	proxy, err := npipes.New(proxyPath, "", 0)
	if err != nil {
		return nil, errors.Wrapf(err, "could not listen %s", proxyPath)
	}
	go func() {
		<-ctx.Done()
		proxy.Close()
	}()
	go func() { s.handleConnections(ctx, proxy) }()
	return &proxyService{
		proxy:     proxy,
		openPorts: initPortTracker(ctx),
	}, nil
}

func (s *proxyService) handleConnections(ctx context.Context, proxy net.Listener) {
	for {
		// Accept new connections from the proxy
		c, err := proxy.Accept()
		if err == io.EOF {
			// Connection was closed
			return
		}
		if err != nil {
			logrus.Errorf("Encountered error while trying to get connection to proxy")
			return
		}
		// Close the proxy connection when context is complete or connection is done
		done := make(chan bool, 1)
		go func() {
			select {
			case <-ctx.Done():
				c.Close()
				return
			case <-done:
				c.Close()
				return
			}
		}()
		// Handle connection
		go func() { s.handleConnection(ctx, c, done) }()
	}
}

func (s *proxyService) handleConnection(ctx context.Context, c net.Conn, done chan bool) {
	// Terminate goroutine watching for context when process is complete
	defer func() { done <- true }()
	// Get the IP address for the host network
	hostNetworkAddress, err := net.ResolveIPAddr("ip", "127.0.0.1")
	if err != nil {
		logrus.Errorf("Encountered error trying to resolve host network address: %v", err)
		return
	}
	// Get a connection to the host network
	hnConn, err := net.DialIP("ip", hostNetworkAddress, hostNetworkAddress)
	if err != nil {
		logrus.Errorf("Encountered error while trying to get a connection to the host network: %v", err)
		return
	}
	defer hnConn.Close()

	// Read incoming request from server
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err == io.EOF {
		// Connection was closed
		return
	}
	if err != nil {
		logrus.Errorf("Encountered error while trying to read request from named pipe: %v", err)
		return
	}
	if n < 4 {
		logrus.Errorf("Unable to identify destination port; packet is less than 4 bytes", err)
		return
	}
	// In a TCP or UDP packet, the third and fourth byte represent the destination port as a uint16
	destPort := binary.BigEndian.Uint16(buf[2:4])
	// Check if destination port is open
	if !s.openPorts.isRegistered(destPort) {
		// ignore connections that come from non-open ports
		return
	}
	// Write to host network
	_, err = hnConn.WriteTo(buf, hostNetworkAddress)
	if err != nil {
		logrus.Errorf("Encountered error while trying to write request to host network: %v", err)
		return
	}
	// Set a deadline to get a response from the proxy
	if err := hnConn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logrus.Errorf("Unable to set deadline on read: %v", err)
		return
	}
	// Read from host network
	buf = make([]byte, 1024)
	_, err = hnConn.Read(buf)
	if err == io.EOF {
		// No response
		return
	}
	if err != nil {
		logrus.Errorf("Encountered error while trying to read response from host network: %v", err)
		return
	}
	// Write back to server
	_, err = c.Write(buf)
	if err != nil {
		logrus.Errorf("Encountered error while trying to send response to named pipe: %v", err)
		return
	}
}

func (s *proxyService) KeepAlive(stream types.ProxyService_KeepAliveServer) (respErr error) {
	defer panics.DealWith(func(recoverObj interface{}) {
		respErr = status.Errorf(codes.Unknown, "panic %v", recoverObj)
	})

	defer stream.SendAndClose(&types.Void{})

	req, err := stream.Recv()
	if err == io.EOF {
		// connection was closed
		return nil
	}
	if err != nil {
		return err
	}

	// Register ports
	for _, port := range req.Ports {
		if port.Port >= 0xFFFF {
			return fmt.Errorf("Invalid port number provided: %d", port.Port)
		}
		s.openPorts.register(uint16(port.Port))
	}

	// Release ports when the connection is lost
	defer func() {
		for _, port := range req.Ports {
			s.openPorts.unregister(uint16(port.Port))
		}
	}()

	// Keep alive
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
