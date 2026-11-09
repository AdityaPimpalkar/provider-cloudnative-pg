// package main implements the provider-cloudnative-pg provider.
package main

import (
	"flag"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/openeverest/openeverest/v2/provider-runtime/reconciler"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/provider"
)

func main() {
	var admission bool
	flag.BoolVar(&admission, "admission", true, "Serve the Instance admission webhook, with the serving certificate the chart mounts.")
	flag.Parse()

	l := ctrl.Log.WithName("setup")
	ctx := ctrl.SetupSignalHandler()

	p := provider.New()

	opts := []reconciler.ReconcilerOption{
		// Serve the health and readiness probes.
		reconciler.WithServer(reconciler.ServerConfig{Port: 8082}),
	}
	if admission {
		opts = append(opts, reconciler.WithAdmission())
	}

	r, err := reconciler.New(ctx, p, opts...)
	if err != nil {
		l.Error(err, "unable to create reconciler")
		os.Exit(1)
	}

	if err := r.Start(ctx); err != nil {
		l.Error(err, "unable to start reconciler")
		os.Exit(1)
	}
}
