package router_test

import (
	"testing"

	"github.com/temporalio/temporal-proxy/internal/config"
	"github.com/temporalio/temporal-proxy/internal/router"
)

// BenchmarkMux_Switch measures rule evaluation for the shapes dev/config.yaml
// and the README describe: a namespace rule that matches, one that falls
// through, and a metadata-keyed rule.
func BenchmarkMux_Switch(b *testing.B) {
	mux := router.MuxFor(prepared(b, config.Routing{
		DefaultUpstream: "primary",
		SystemUpstream:  "system",
		Rules: []config.RoutingRule{
			{Upstream: "prod", Match: config.RoutingMatch{Namespace: "prod-*"}},
			{Upstream: "staging", Match: config.RoutingMatch{Namespace: "staging-*"}},
			{Upstream: "cluster-3", Match: config.RoutingMatch{Metadata: map[string]string{"x-cluster": "3"}}},
		},
	}))

	b.Run("namespace-match", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if up, _ := mux.Switch("prod-eu-1", nil); up != "prod" {
				b.Fatalf("routed to %q", up)
			}
		}
	})

	b.Run("metadata-match", func(b *testing.B) {
		md := map[string][]string{"x-cluster": {"3"}}
		b.ReportAllocs()
		for b.Loop() {
			if up, _ := mux.Switch("other-ns", md); up != "cluster-3" {
				b.Fatalf("routed to %q", up)
			}
		}
	})

	b.Run("default", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if up, _ := mux.Switch("tenant-x", nil); up != "primary" {
				b.Fatalf("routed to %q", up)
			}
		}
	})

	b.Run("system", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if up, _ := mux.Switch("", nil); up != "system" {
				b.Fatalf("routed to %q", up)
			}
		}
	})
}
