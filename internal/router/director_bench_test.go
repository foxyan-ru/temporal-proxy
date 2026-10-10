package router_test

import (
	"testing"

	"google.golang.org/grpc"

	"github.com/temporalio/temporal-proxy/internal/config"
	"github.com/temporalio/temporal-proxy/internal/router"
)

// BenchmarkDirector_Resolve measures the routing decision including the
// decision metric: the real MuxFor mux and a Prometheus-backed Reporter on a
// fresh registry, with the handlers map the dataplane would hold. The logger
// is nil so Debug entries take the same disabled path as production's default
// log level.
func BenchmarkDirector_Resolve(b *testing.B) {
	mux := router.MuxFor(prepared(b, config.Routing{
		DefaultUpstream: "primary",
		Rules: []config.RoutingRule{
			{Upstream: "prod", Match: config.RoutingMatch{Namespace: "prod-*"}},
		},
	}))

	rep, _ := newTestReporter(b, "primary", "prod")
	handlers := map[string]grpc.StreamHandler{
		"primary": func(any, grpc.ServerStream) error { return nil },
		"prod":    func(any, grpc.ServerStream) error { return nil },
	}
	d := router.NewDirector(mux, handlers, rep, nil)

	b.Run("namespace-match", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			target, err := d.Resolve(b.Context(), "/svc/Method", "prod-eu-1", nil)
			if err != nil {
				b.Fatal(err)
			}
			if target.Upstream != "prod" {
				b.Fatalf("routed to %q", target.Upstream)
			}
		}
	})

	b.Run("default", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			target, err := d.Resolve(b.Context(), "/svc/Method", "tenant-x", nil)
			if err != nil {
				b.Fatal(err)
			}
			if target.Upstream != "primary" {
				b.Fatalf("routed to %q", target.Upstream)
			}
		}
	})
}
