package powershell

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Sourced from https://github.com/flannel-io/flannel/blob/d31b0dc85a5a15bda5e606acbbbb9f7089441a87/pkg/powershell/powershell.go

// RunCommand executes a given powershell command.
//
// When the command throws a powershell exception, RunCommand will return the exception message as error.
func RunCommand(command string) ([]byte, error) {
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf(commandWrapper, command))

	stdout, err := cmd.Output()
	if err != nil {
		if cmd.ProcessState.ExitCode() != 0 {
			message := strings.TrimSpace(string(stdout))
			return []byte{}, errors.New(message)
		}

		return []byte{}, err
	}

	return stdout, nil
}

// RunCommandf executes a given powershell command. Command argument formats according to a format specifier (See fmt.Sprintf).
//
// When the command throws a powershell exception, RunCommandf will return the exception message as error.
func RunCommandf(command string, a ...interface{}) ([]byte, error) {
	return RunCommand(fmt.Sprintf(command, a...))
}
