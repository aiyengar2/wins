package proxy

import (
	"fmt"

	"github.com/rancher/wins/pkg/defaults"
	"github.com/urfave/cli"
)

func NewCommand() cli.Command {
	return cli.Command{
		Name:   "proxy",
		Usage:  fmt.Sprintf("Set up a proxy for a TCP or UDP port via %s", defaults.WindowsServiceDisplayName),
		Flags:  _proxyFlags,
		Before: _proxyRequestParser,
		Action: _proxyAction,
	}
}
