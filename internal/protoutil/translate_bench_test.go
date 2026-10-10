package protoutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/temporalio/temporal-proxy/internal/protoutil"
)

// Benchmarks for Translator.Translate on the two message shapes the proxy
// rewrites per request: a namespaced command (request direction) and a
// DescribeNamespace response, which reaches NamespaceInfo.name only through
// the override list.

func BenchmarkTranslator_StartWorkflowRequest(b *testing.B) {
	tr := protoutil.NewTranslator(protoregistry.GlobalFiles)
	require.NoError(b, tr.WarmService(wfService))
	req := benchStartRequest()

	b.ReportAllocs()
	for b.Loop() {
		// The mapper returns a fixed name so repeated in-place rewrites stay
		// stable and the benchmark measures the walk, not string growth.
		tr.Translate(req, func(string) string { return "payments-production-remote" })
	}
}

func BenchmarkTranslator_StartWorkflowRequestCold(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		// A fresh Translator per iteration pays plan construction on the first
		// use, which is what an operator skipping WarmService pays per type.
		tr := protoutil.NewTranslator(protoregistry.GlobalFiles)
		tr.Translate(benchStartRequest(), func(string) string { return "remote" })
	}
}

func BenchmarkTranslator_DescribeNamespaceResponse(b *testing.B) {
	tr := protoutil.NewTranslator(protoregistry.GlobalFiles)
	require.NoError(b, tr.WarmService(wfService))
	resp := &workflowservice.DescribeNamespaceResponse{
		NamespaceInfo: &namespace.NamespaceInfo{Name: "payments-production"},
	}
	// WarmService stores a plan per top-level method input/output type, so the
	// response type itself is what IsWarm can see; NamespaceInfo's plan lives
	// inside the response plan's child graph.
	require.True(b, tr.IsWarm(resp.ProtoReflect().Descriptor().FullName()), "plan for DescribeNamespaceResponse must be warm through WarmService")

	b.ReportAllocs()
	for b.Loop() {
		tr.Translate(resp, func(string) string { return "payments-production-local" })
	}
}
