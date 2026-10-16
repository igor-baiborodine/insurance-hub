package telemetry

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPPropagation_ExtractsLegacyAndModernParents(t *testing.T) {
	// given
	provider := enabledProvider(t, newRecordingExporter(), time.Second)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	const w3cTrace = "11111111111111111111111111111111"
	const b3Trace = "22222222222222222222222222222222"
	const b3Span = "3333333333333333"
	w3c := "00-" + w3cTrace + "-4444444444444444-01"
	cases := []struct {
		name, traceID, spanID string
		sampled               bool
		headers               http.Header
	}{
		{"w3c", w3cTrace, "4444444444444444", true, http.Header{
			"Traceparent": {w3c}, "Tracestate": {"vendor=value"},
		}},
		{"unsampled w3c", w3cTrace, "4444444444444444", false, http.Header{
			"Traceparent": {"00-" + w3cTrace + "-4444444444444444-00"},
		}},
		{"b3 multi 128", b3Trace, b3Span, true, http.Header{
			"X-B3-Traceid": {b3Trace}, "X-B3-Spanid": {b3Span}, "X-B3-Sampled": {"1"},
		}},
		{
			"b3 multi 64",
			"0000000000000000" + "1234567890abcdef",
			b3Span,
			true,
			http.Header{
				"X-B3-Traceid": {
					"1234567890abcdef",
				},
				"X-B3-Spanid":  {b3Span},
				"X-B3-Sampled": {"1"},
			},
		},
		{
			"b3 single",
			b3Trace,
			b3Span,
			true,
			http.Header{"B3": {b3Trace + "-" + b3Span + "-1"}},
		},
		{"valid b3 wins w3c", b3Trace, b3Span, true, http.Header{
			"Traceparent": {w3c}, "X-B3-Traceid": {b3Trace},
			"X-B3-Spanid": {b3Span}, "X-B3-Sampled": {"1"},
		}},
		{"single wins multi", b3Trace, b3Span, true, http.Header{
			"B3":           {b3Trace + "-" + b3Span + "-1"},
			"X-B3-Traceid": {w3cTrace}, "X-B3-Spanid": {"5555555555555555"},
			"X-B3-Sampled": {"0"},
		}},
		{"malformed single falls back to multi", b3Trace, b3Span, true, http.Header{
			"B3": {"invalid"}, "X-B3-Traceid": {b3Trace},
			"X-B3-Spanid": {b3Span}, "X-B3-Sampled": {"1"},
		}},
		{"malformed b3 falls back to w3c", w3cTrace, "4444444444444444", true, http.Header{
			"Traceparent": {w3c}, "B3": {"invalid"}, "X-B3-Traceid": {"invalid"},
		}},
		{"unsampled b3", b3Trace, b3Span, false, http.Header{
			"X-B3-Traceid": {b3Trace}, "X-B3-Spanid": {b3Span}, "X-B3-Sampled": {"0"},
		}},
		{"deferred b3", b3Trace, b3Span, false, http.Header{
			"X-B3-Traceid": {b3Trace}, "X-B3-Spanid": {b3Span},
		}},
		{"debug b3", b3Trace, b3Span, true, http.Header{
			"X-B3-Traceid": {b3Trace}, "X-B3-Spanid": {b3Span}, "X-B3-Flags": {"1"},
		}},
		{"absent", "", "", true, nil},
		{
			"malformed",
			"",
			"",
			true,
			http.Header{"Traceparent": {"invalid"}, "B3": {"invalid"}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// when
			ctx := provider.HTTPPropagator().
				Extract(context.Background(), propagation.HeaderCarrier(test.headers))
			parent := trace.SpanContextFromContext(ctx)
			_, child := provider.TracerProvider().
				Tracer("continuity-test").
				Start(ctx, "child")
			defer child.End()

			// then
			if got := parent.TraceID().
				String(); test.traceID != "" &&
				got != test.traceID {
				t.Errorf("parent trace ID = %s, want %s", got, test.traceID)
			}
			if got := parent.SpanID().
				String(); test.spanID != "" &&
				got != test.spanID {
				t.Errorf("parent span ID = %s, want %s", got, test.spanID)
			}
			if test.traceID == "" && parent.IsValid() {
				t.Errorf("unexpected remote parent: %s", parent.TraceID())
			}
			if test.traceID != "" && !parent.IsRemote() {
				t.Error("extracted parent is not remote")
			}
			if test.name == "w3c" && parent.TraceState().String() != "vendor=value" {
				t.Errorf("W3C tracestate = %q", parent.TraceState().String())
			}
			if baggage.FromContext(ctx).Len() != 0 {
				t.Error("arbitrary baggage was extracted")
			}
			if child.IsRecording() != test.sampled {
				t.Errorf(
					"child recording = %t, want %t",
					child.IsRecording(),
					test.sampled,
				)
			}
			if test.traceID != "" && child.SpanContext().TraceID() != parent.TraceID() {
				t.Error("child detached from remote trace")
			}
		})
	}
}

func TestHTTPPropagation_InjectsW3CAndB3MultiFromOneContext(t *testing.T) {
	// given
	provider := enabledProvider(t, newRecordingExporter(), time.Second)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	_, span := provider.TracerProvider().
		Tracer("continuity-test").
		Start(context.Background(), "client")
	defer span.End()
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	// when
	provider.HTTPPropagator().
		Inject(trace.ContextWithSpan(request.Context(), span), propagation.HeaderCarrier(request.Header))

	// then
	if got := request.Header.Get("X-B3-Traceid"); got != span.SpanContext().TraceID().String() {
		t.Errorf("B3 trace ID = %q", got)
	}
	if got := request.Header.Get("X-B3-Spanid"); got != span.SpanContext().SpanID().String() {
		t.Errorf("B3 span ID = %q", got)
	}
	if got := request.Header.Get("X-B3-Sampled"); got != "1" {
		t.Errorf("B3 sampled = %q", got)
	}
	if got := request.Header.Get(
		"Traceparent",
	); got != "00-"+span.SpanContext().
		TraceID().
		String()+
		"-"+span.SpanContext().
		SpanID().
		String()+
		"-01" {
		t.Errorf("W3C traceparent = %q", got)
	}
	if request.Header.Get("B3") != "" || request.Header.Get("Baggage") != "" {
		t.Error("unexpected single B3 or baggage injection")
	}
}

func TestGRPCPropagation_KeepsW3COnly(t *testing.T) {
	// given
	provider := enabledProvider(t, newRecordingExporter(), time.Second)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	const traceID = "11111111111111111111111111111111"
	const spanID = "2222222222222222"
	b3Headers := propagation.MapCarrier{
		"x-b3-traceid": traceID, "x-b3-spanid": spanID, "x-b3-sampled": "1",
	}
	w3cHeaders := propagation.MapCarrier{
		"traceparent": "00-" + traceID + "-" + spanID + "-01",
	}

	// when
	b3Parent := trace.SpanContextFromContext(provider.GRPCPropagator().Extract(
		context.Background(), b3Headers,
	))
	w3cParent := trace.SpanContextFromContext(provider.GRPCPropagator().Extract(
		context.Background(), w3cHeaders,
	))

	// then
	if b3Parent.IsValid() {
		t.Errorf("gRPC extracted B3 parent: %s", b3Parent.TraceID())
	}
	if w3cParent.TraceID().String() != traceID || w3cParent.SpanID().String() != spanID ||
		!w3cParent.IsSampled() {
		t.Errorf(
			"gRPC W3C parent = %s/%s sampled=%t",
			w3cParent.TraceID(),
			w3cParent.SpanID(),
			w3cParent.IsSampled(),
		)
	}
}
