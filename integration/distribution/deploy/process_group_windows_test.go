//go:build windows

package deploy_test

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func terminateProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := command.Process.Kill()
	_ = command.Wait()
	return err
}
