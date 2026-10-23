//go:build !windows

package main

import "context"

func runServe(opts serveOptions) error {
	ctx, stop := signalContext(context.Background())
	defer stop()
	return serveCommand(ctx, opts)
}
