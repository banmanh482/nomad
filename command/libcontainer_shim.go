package command

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/opencontainers/runc/libcontainer"
	_ "github.com/opencontainers/runc/libcontainer/nsenter"
)

type LibcontainerShimCommand struct {
	Meta
}

func (e *LibcontainerShimCommand) Help() string {
	helpText := `
	This is a command used by Nomad internally to use libcontainer"
	`
	return strings.TrimSpace(helpText)
}

func (e *LibcontainerShimCommand) Synopsis() string {
	return "internal - libcontainer shim"
}

func (e *LibcontainerShimCommand) Run(args []string) int {
	runtime.GOMAXPROCS(1)
	runtime.LockOSThread()
	factory, _ := libcontainer.New("")
	if err := factory.StartInitialization(); err != nil {
		// as the error is sent back to the parent there is no need to log
		// or write it to stderr because the parent process will handle this
		e.Meta.Ui.Error(fmt.Sprintf("---------------- %v: %v; %v; %v", err, os.Args, os.Environ(), os.Getuid()))
		os.Exit(1)
	}
	panic("libcontainer: container init failed to exec")
	return 1
}
