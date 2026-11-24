package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c
}

func setProcGroup(c *exec.Cmd) {}

func killTree(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = hiddenCmd("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)).Run()
	_ = c.Process.Kill()
}
