package upgrade

import (
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/rancher/wins/cmd/upgrade/internal/powershell"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const upgradePS1Fmt = `& {
$ErrorActionPreference = "Stop";

$newBinPath = "%[1]s"
$winsArgs = "%[2]s"

$winsProcess = (Get-CimInstance Win32_Service -Filter 'Name = "rancher-wins"')

if ($winsProcess -ne $null) {
	$currBinPath = $winsProcess.PathName.Split(' ')[0]
	if ($currBinPath -ne $newBinPath) {
		Get-Service -Name rancher-wins | Stop-Service;
		sc.exe delete rancher-wins
		Copy-Item -Recurse -Force -Path $newBinPath -Destination $currBinPath | Out-Null
	}
} else {
	$currBinPath = $newBinPath
}

"$currBinPath srv app run $winsArgs" | iex

Start-Service -Name rancher-wins;
}`

const restartServicePS1 = `& {
	$ErrorActionPreference = "Stop";

	$winsProcess = (Get-CimInstance Win32_Service -Filter 'Name = "rancher-wins"')
	if ($winsProcess -ne $null) {
		"$winsProcess" | iex
		Start-Service -Name rancher-wins;
	}
}`

var _upgradeFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "binary",
		Usage: "[optional] Name of binary in working directory to upgrade to",
		Value: os.Args[0], // default behavior is to use current binary
	},
	cli.StringFlag{
		Name:  "wins-args",
		Usage: "[optional] Arguments to pass onto wins srv app run",
		Value: "--register",
	},
	cli.BoolFlag{
		Name:  "debug",
		Usage: "[optional] whether to print debugging logs from performing the upgrade",
	},
}

func _upgradeRequestParser(cliCtx *cli.Context) (err error) {
	// validate
	binPath := cliCtx.String("binary")
	stat, err := os.Stat(binPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return errors.Wrapf(err, "cannot find --binary %s", binPath)
		}
		return nil
	} else if stat.IsDir() {
		return errors.New("expected file for binary, found directory")
	}
	winsArgs := cliCtx.String("wins-args")
	if strings.Contains(winsArgs, "--unregister") {
		return fmt.Errorf(`cannot provide "--unregister" to --wins-args`)
	}
	if !strings.Contains(winsArgs, "--register") {
		return fmt.Errorf(`must provide a string containing "--register" to --win-args`)
	}
	return nil
}

func _upgradeAction(cliCtx *cli.Context) (err error) {
	if cliCtx.Bool("debug") {
		logrus.SetLevel(logrus.DebugLevel)
	}
	binPath := cliCtx.String("binary")
	winsArgs := cliCtx.String("wins-args")
	out, err := powershell.RunCommandf(upgradePS1Fmt, binPath, winsArgs)
	if len(out) > 0 {
		logrus.Debugf("logs from upgrade.ps1 \n%s\nEOF", out)
	}
	if err != nil {
		logrus.Error("upgrade failed, attempting to ensure rancher-wins service is not stopped: %v", err)
		out, restartServiceErr := powershell.RunCommand(restartServicePS1)
		if len(out) > 0 {
			logrus.Debugf("logs from upgrade.ps1 \n%s\nEOF", out)
		}
		if err != nil {
			logrus.Errorf("unable to restart rancher-wins service: %v", restartServiceErr)
		}
		return err
	}
	logrus.Info("upgrade succeeded")
	return nil
}
