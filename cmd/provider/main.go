// package main implements the provider-cloudnative-pg provider.
package main

import (
	"os"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/openeverest/openeverest/v2/provider-runtime/reconciler"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/provider"
)

// main is the entry point for the provider.
func main() {
	l := ctrl.Log.WithName("setup")
	ctx := ctrl.SetupSignalHandler()

	p := provider.New()

	r, err := reconciler.New(ctx, p,
		// Enable HTTP server for validation and schema endpoints.
		reconciler.WithServer(reconciler.ServerConfig{
			Port:           8082,
			ValidationPath: "/validate",
		}),
	)
	if err != nil {
		l.Error(err, "unable to create reconciler")
		os.Exit(1)
	}

	if err := r.Start(ctx); err != nil {
		l.Error(err, "unable to start reconciler")
		os.Exit(1)
	}
}
