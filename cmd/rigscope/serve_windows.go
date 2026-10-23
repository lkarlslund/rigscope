//go:build windows

package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"
)

// runServe runs the daemon, under the Service Control Manager when Windows
// started the process as a service.
func runServe(opts serveOptions) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		ctx, stop := signalContext(context.Background())
		defer stop()
		return serveCommand(ctx, opts)
	}
	return svc.Run(serviceName, &windowsService{opts: opts})
}

type windowsService struct {
	opts serveOptions
}

func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveCommand(ctx, s.opts)
	}()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			return serviceExit(err)
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				return serviceExit(<-done)
			}
		}
	}
}

// serviceExit reports a serve error as a service-specific exit code, which
// makes the SCM apply the restart recovery actions.
func serviceExit(err error) (bool, uint32) {
	if err == nil {
		return false, 0
	}
	_, _ = fmt.Fprintln(os.Stderr, "rigscope:", err)
	return true, 1
}
