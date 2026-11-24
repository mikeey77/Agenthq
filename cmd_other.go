//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func hiddenCmd(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func setProcGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killTree(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	_ = c.Process.Kill()
}
