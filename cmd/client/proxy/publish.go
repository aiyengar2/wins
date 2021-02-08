package proxy

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
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
	"google.golang.org/grpc"
)

var _proxyFlags = internal.NewGRPCClientConn([]cli.Flag{
	cli.GenericFlag{
		Name:  "publish",
		Usage: "[required] [list-argument] Publish a port or a range of ports, e.g.: TCP:443 UDP:4789-4790",
		Value: flags.NewListValue(),
	},
	cli.GenericFlag{
		Name:  "name",
		Value: flags.NewListValue(),
	},
	cli.StringFlag{
		Name:  "proxy",
		Usage: "[optional] Specifies the name of the proxy listening named pipe",
		Value: defaults.ProxyPipeName,
	},
	cli.StringFlag{
		Name:  "identifier",
		Usage: "[optional] Specifies an identifier to use for the proxy's connection (max length 256). Defaults to hostname",
	},
})

var _proxyKeepAliveRequest *types.ProxyKeepAliveRequest

func _proxyRequestParser(cliCtx *cli.Context) (err error) {
	// Add ports
	var publishPorts []*types.Port
	if publishList := flags.GetListValue(cliCtx, "publish"); !publishList.IsEmpty() {
		publishPorts, err := parsePublishes(publishList.Get())
		if err != nil {
			return errors.Wrapf(err, "failed to parse --publish %s", publishPorts)
		}
	}

	// Use hostname as identifier
	id := cliCtx.String("identifier")
	if len(id) == 0 {
		id, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("failed to get hostname to use as identifier for proxy")
		}
	}
	if len(id) > 256 {
		return fmt.Errorf("--identifier %s is %d bytes long (maximum 256 bytes)", id, len(id))
	}
	_proxyKeepAliveRequest = &types.ProxyKeepAliveRequest{
		ID:    id,
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
			number, err := strconv.ParseUint(portRanges[0], 10, 16)
			if err != nil {
				return nil, errors.Wrapf(err, "could not parse port %s from expose %s", portRanges[0], pub)
			}

			proxyPublishes = append(proxyPublishes, &types.Port{
				Protocol: types.Protocol(types.Protocol_value[protocol]),
				Port:     uint32(number),
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
				if number > 0xFF {
					return nil, errors.Errorf("invalid port number: %d", number)
				}
				proxyPublishes = append(proxyPublishes, &types.Port{
					Protocol: types.Protocol(types.Protocol_value[protocol]),
					Port:     uint32(number),
				})
			}
		} else {
			return nil, errors.Errorf("could not parse expose %s", pub)
		}
	}

	return proxyPublishes, nil
}

func _proxyKeepAlive(ctx context.Context, grpcClientConn *grpc.ClientConn) error {
	client := types.NewProxyServiceClient(grpcClientConn)
	keepAliveStream, err := client.KeepAlive(ctx)
	if err != nil {
		return err
	}
	doneC := keepAliveStream.Context().Done()
	for {
		select {
		case <-doneC:
			return nil
		default:
		}
		if err := keepAliveStream.Send(_proxyKeepAliveRequest); err != nil {
			logrus.Error("Failed to keep alive: %v", err)
			return nil
		}
		time.Sleep(5 * time.Minute)
	}
}

func _proxyAction(cliCtx *cli.Context) (err error) {
	defer panics.Log()

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

	// Set up OS signals
	signals := make(chan os.Signal, 1<<10)
	go func() {
		select {
		case <-signals:
			cancel()
			return
		case <-ctx.Done():
			return
		}
	}()
	signal.Notify(signals, syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM)

	// Set up proxy
	proxy := cliCtx.String("proxy")
	proxyPath := npipes.GetFullPath(proxy)
	npipeDialer, err := npipes.NewDialer(proxyPath, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("Failed to get dialer for named pipe at %s: %v", proxyPath, err)
	}
	p, err := initProxy(ctx, _proxyKeepAliveRequest.ID, npipeDialer)
	if err != nil {
		return err
	}
	for _, port := range _proxyKeepAliveRequest.Ports {
		if port.Protocol == types.Protocol_TCP {
			p.listen("ip4", "tcp", uint16(port.Port))
			p.listen("ip6", "tcp", uint16(port.Port))
		} else {
			p.listen("ip4", "udp", uint16(port.Port))
			p.listen("ip6", "udp", uint16(port.Port))
		}
	}
	defer p.close()

	return _proxyKeepAlive(ctx, grpcClientConn)
}
