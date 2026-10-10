package protoutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/temporalio/temporal-proxy/internal/protoutil"
)

// benchStartRequest builds a StartWorkflowExecutionRequest shaped like the ones
// SDK clients send: identity fields, a typed workflow, and a 1 KiB input
// payload, so extraction measures a realistic unmarshal rather than a
// two-field toy message.
func benchStartRequest() *workflowservice.StartWorkflowExecutionRequest {
	input := make([]byte, 1024)
	for i := range input {
		input[i] = byte('a' + i%26)
	}

	return &workflowservice.StartWorkflowExecutionRequest{
		Namespace:    "payments-production",
		WorkflowId:   "billing-run-2026-10-10-abc123",
		WorkflowType: &common.WorkflowType{Name: "billing.daily-run"},
		TaskQueue:    &common.TaskQueue{Name: "billing-tq"},
		Input: &common.Payloads{Payloads: []*common.Payload{{
			Metadata: map[string][]byte{"encoding": []byte("json/plain")},
			Data:     input,
		}}},
		Identity: "1@worker-7",
	}
}

func benchMarshal(b *testing.B, m proto.Message) []byte {
	b.Helper()

	payload, err := proto.Marshal(m)
	require.NoError(b, err)
	return payload
}

func BenchmarkExtractor_NamespacedRequest(b *testing.B) {
	payload := benchMarshal(b, benchStartRequest())
	ex := protoutil.NewExtractor(protoregistry.GlobalFiles, protoregistry.GlobalTypes)
	require.Equal(b, "payments-production", ex.Namespace(startMethod, payload), "warm-up call must extract the namespace")

	b.ReportAllocs()
	for b.Loop() {
		if ns := ex.Namespace(startMethod, payload); ns == "" {
			b.Fatal("namespace was not extracted")
		}
	}
}

// BenchmarkExtractor_SystemRequest covers the request the SDK sends on every
// connect: no namespace accessor, so the full unmarshal runs and the cast
// misses.
func BenchmarkExtractor_SystemRequest(b *testing.B) {
	payload := benchMarshal(b, &workflowservice.GetSystemInfoRequest{})
	ex := protoutil.NewExtractor(protoregistry.GlobalFiles, protoregistry.GlobalTypes)
	require.Equal(b, "", ex.Namespace(systemMethod, payload))

	b.ReportAllocs()
	for b.Loop() {
		if ns := ex.Namespace(systemMethod, payload); ns != "" {
			b.Fatal("system request unexpectedly carried a namespace")
		}
	}
}
