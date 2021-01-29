package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rancher/wins/cmd/client/internal"
	"github.com/rancher/wins/cmd/cmds/flags"
	"github.com/rancher/wins/pkg/defaults"
	"github.com/rancher/wins/pkg/npipes"
	"github.com/rancher/wins/pkg/panics"
	"github.com/rancher/wins/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

var _proxyFlags = internal.NewGRPCClientConn([]cli.Flag{
	cli.GenericFlag{
		Name:  "publish",
		Usage: "[required] [list-argument] Publish a port or a range of ports, e.g.: TCP:443 UDP:4789-4790",
		Value: flags.NewListValue(),
	},
	cli.StringFlag{
		Name:  "proxy",
		Usage: "[optional] Specifies the name of the proxy listening named pipe",
		Value: defaults.ProxyPipeName,
	},
})

var _proxyPublishRequest *types.ProxyPublishRequest

func _proxyRequestParser(cliCtx *cli.Context) (err error) {
	// validate
	var publishPorts []*types.Port
	if publishList := flags.GetListValue(cliCtx, "publish"); !publishList.IsEmpty() {
		publishPorts, err := parsePublishes(publishList.Get())
		if err != nil {
			return errors.Wrapf(err, "failed to parse --publish %s", publishPorts)
		}
	}

	_proxyPublishRequest = &types.ProxyPublishRequest{
		Ports: publishPorts,
	}

	return nil
}

func parsePublishes(publishPorts []string) ([]*types.Port, error) {
	var proxyPublishes []*types.Port
	for _, pub := range publishPorts {
		publishes := strings.SplitN(pub, ":", 2)
		if len(publishes) != 2 {
			return nil, errors.Errorf("could not parse publish %s", publishes)
		}

		protocol := publishes[0]
		portRanges := strings.SplitN(publishes[1], "-", 2)
		if len(portRanges) == 1 {
			number, err := strconv.Atoi(portRanges[0])
			if err != nil {
				return nil, errors.Wrapf(err, "could not parse port %s from expose %s", portRanges[0], pub)
			}

			proxyPublishes = append(proxyPublishes, &types.Port{
				Protocol: types.Protocol(types.Protocol_value[protocol]),
				Port:     int32(number),
			})
		} else if len(portRanges) == 2 {
			low, err := strconv.Atoi(portRanges[0])
			if err != nil {
				return nil, errors.Wrapf(err, "could not parse port %s from expose %s", portRanges[0], pub)
			}
			high, err := strconv.Atoi(portRanges[1])
			if err != nil {
				return nil, errors.Wrapf(err, "could not parse port %s from expose %s", portRanges[1], pub)
			}
			if low >= high {
				return nil, errors.Errorf("could not accept the range %d - %d from expose %s", low, high, pub)
			}

			for number := low; number <= high; number++ {
				proxyPublishes = append(proxyPublishes, &types.Port{
					Protocol: types.Protocol(types.Protocol_value[protocol]),
					Port:     int32(number),
				})
			}
		} else {
			return nil, errors.Errorf("could not parse expose %s", pub)
		}
	}

	return proxyPublishes, nil
}

func _proxyTCPToNamedPipe(ctx context.Context, npipeDialer npipes.Dialer, address string) error {
	// Setup listener at TCP port
	l, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("Failed to create TCP server listening at %s: %v", address, err)
	}
	defer l.Close()
	// Close TCP listener when the context is complete
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	// Check if conection can be made to wins proxy named pipe
	proxy, err := npipeDialer(ctx, "")
	if err != nil {
		return fmt.Errorf("Failed to open connection with the wins proxy named pipe: %v", err)
	}
	proxy.Close()

	for {
		// Wait for a connection
		c, err := l.Accept()
		if err == io.EOF {
			// Connection was closed
			return nil
		}
		if err != nil {
			return fmt.Errorf("Encountered error while trying to accept a new connection at %s: %s", address, err)
		}

		// Close TCP connection when context is complete or connection is done
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
		go func() {
			// Setup connection to wins proxy named pipe
			proxy, err := npipeDialer(ctx, "")
			if err != nil {
				logrus.Errorf("Failed to open connection with the wins proxy named pipe: %v", err)
				return
			}
			defer proxy.Close()

			for {
				// Read incoming request from server
				buf := make([]byte, 1024)
				_, err := c.Read(buf)
				if err == io.EOF {
					// Connection was closed
					break
				}
				if err != nil {
					logrus.Errorf("Encountered error while trying to read request from TCP server: %v", err)
					continue
				}
				// Write incoming request to proxy
				_, err = proxy.Write(buf)
				if err != nil {
					logrus.Errorf("Encountered error while trying to write request to proxy pipe: %v", err)
					continue
				}
				// Read outgoing response from proxy
				buf = make([]byte, 1024)
				_, err = proxy.Read(buf)
				if err == io.EOF {
					continue
				}
				if err != nil {
					logrus.Errorf("Encountered error while trying to read response from proxy pipe: %v", err)
					continue
				}
				// Write outgoing response to client
				_, err = c.Write(buf)
				if err != nil {
					logrus.Errorf("Encountered error while trying to write request to TCP client: %v", err)
					continue
				}
			}
			done <- true
		}()
	}
}

func _proxyUDPToNamedPipe(ctx context.Context, npipeDialer npipes.Dialer, address string) error {
	// Setup packet connection listening at UDP address
	pc, err := net.ListenPacket("udp", address)
	if err != nil {
		return fmt.Errorf("Failed to create UDP server listening at %s: %v", address, err)
	}
	defer pc.Close()

	// Setup connection to wins proxy named pipe
	proxy, err := npipeDialer(ctx, "")
	if err != nil {
		return fmt.Errorf("Failed to open connection with the wins proxy named pipe: %v", err)
	}
	defer proxy.Close()

	// Close packet connection and npipe connection when the context is complete
	go func() {
		<-ctx.Done()
		pc.Close()
		proxy.Close()
	}()

	for {
		// Read incoming request from server
		buf := make([]byte, 1024)
		n, returnAddr, packetErr := pc.ReadFrom(buf)
		if packetErr == io.EOF {
			// Packet connection was closed
			break
		}
		// Process error only after writing
		if packetErr != nil {
			logrus.Errorf("Encountered error while trying to read packets sent to UDP server: %v", err)
			continue
		}
		// Drop any extra packets
		buf = buf[:n]
		// Write incoming request to proxy
		_, err = proxy.Write(buf)
		if err != nil {
			logrus.Errorf("Encountered error while trying to write request to proxy pipe: %v", err)
			continue
		}
		// Read outgoing response from proxy
		buf = make([]byte, 1024)
		_, err = proxy.Read(buf)
		if err == io.EOF {
			continue
		}
		if err != nil {
			logrus.Errorf("Encountered error while trying to read response from proxy pipe: %v", err)
			continue
		}
		// Write outgoing response to client
		if _, err := pc.WriteTo(buf, returnAddr); err != nil {
			logrus.Errorf("Encountered error while trying to write packets to UDP client: %v", err)
			continue
		}
	}

	return nil
}

func _proxyAction(cliCtx *cli.Context) (err error) {
	defer panics.Log()

	// Set up named pipe client
	proxy := cliCtx.String("proxy")
	proxyPath := npipes.GetFullPath(proxy)
	npipeDialer, err := npipes.NewDialer(proxyPath, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("Failed to connect to proxy named pipe at %s: %v", proxyPath, err)
	}

	// parse grpc client connection
	grpcClientConn, err := internal.ParseGRPCClientConn(cliCtx)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := grpcClientConn.Close()
		if err == nil {
			err = closeErr
		}
	}()

	// Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// initialize client that will tell wins to publish the port
	client := types.NewProxyServiceClient(grpcClientConn)
	publishResp, err := client.Publish(ctx, _proxyPublishRequest)
	if err != nil {
		return err
	}
	if !publishResp.GetSuccess() {
		return fmt.Errorf("Failed to publish port: %v", err)
	}

	// Setup an errgroup
	errs, ctx := errgroup.WithContext(ctx)

	// Create TCP and UDP proxies
	for _, port := range _proxyPublishRequest.Ports {
		address := fmt.Sprintf(":%d", port.Port)
		if port.Protocol == types.Protocol_TCP {
			errs.Go(func() error {
				return _proxyTCPToNamedPipe(ctx, npipeDialer, address)
			})
		} else {
			errs.Go(func() error {
				return _proxyUDPToNamedPipe(ctx, npipeDialer, address)
			})
		}
	}
	// keep pinging wins to keep the ports open
	errs.Go(func() error {
		for {
			time.Sleep(30 * time.Second)
			publishResp, err := client.Publish(ctx, _proxyPublishRequest)
			if err != nil {
				return err
			}
			if !publishResp.GetSuccess() {
				return fmt.Errorf("Failed to keep port published: %v", err)
			}
		}
	})

	return errs.Wait()
}
