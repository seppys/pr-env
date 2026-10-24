/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package githubwebhook receives GitHub pull request webhooks and turns them into PreviewEnvironments.
package githubwebhook

import (
	"context"
	"errors"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// readHeaderTimeout bounds how long a client may take to send its headers, so slow
	// clients can't hold connections open indefinitely.
	readHeaderTimeout = 10 * time.Second
	// shutdownTimeout bounds how long in-flight requests get to finish when the manager stops.
	shutdownTimeout = 10 * time.Second
	namespace       = "default"
)

// Server serves GitHub webhooks. It is a manager Runnable, so it starts and stops with the manager.
type Server struct {
	addr    string
	handler http.Handler
}

// NewServer returns a Server listening on addr that only accepts deliveries signed with secret.
func NewServer(addr string, c client.Client, secret []byte) *Server {
	svc := &service{
		client:    c,
		namespace: namespace,
		configs:   newRawFetcher(),
	}
	handler := &Handler{secret: secret, service: svc}

	mux := http.NewServeMux()
	mux.Handle("POST /webhook", handler)
	return &Server{addr: addr, handler: mux}
}

// Start serves until ctx is cancelled, then shuts down gracefully. A non-nil error (for example,
// the port is already in use) makes the manager stop every component, so the process exits
// instead of running without its webhook endpoint.
func (s *Server) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("github-webhook")

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() {
		<-ctx.Done() // the manager is stopping
		// ctx is already cancelled, so shutdown needs its own deadline.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error(err, "Failed to shut down GitHub webhook server")
		}
	}()

	log.Info("Starting GitHub webhook server", "addr", s.addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
