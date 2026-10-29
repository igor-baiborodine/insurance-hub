package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"unicode"

	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

const redactedValue = "[REDACTED]"

// New constructs a JSON logger with a short service label, attribute redaction and trace
// correlation derived from the logging context.
func New(output io.Writer, serviceName string, level slog.Level) *slog.Logger {
	return newLogger(output, level).With(slog.String("service", serviceName))
}

// NewIdentity adds the six caller-owned identity fields to every log record.
func NewIdentity(
	output io.Writer,
	serviceIdentity identity.Identity,
	level slog.Level,
) *slog.Logger {
	return newLogger(output, level).With(
		slog.String("service_namespace", serviceIdentity.ServiceNamespace),
		slog.String("service_name", serviceIdentity.ServiceName),
		slog.String("service_version", serviceIdentity.ServiceVersion),
		slog.String("service_instance_id", serviceIdentity.ServiceInstance),
		slog.String("deployment_environment_name", serviceIdentity.Environment),
		slog.String("service_implementation", serviceIdentity.Implementation),
	)
}

// LogRequired writes a caller-defined contract record independently of the diagnostic threshold
// when log comes from New or NewIdentity, including a With or WithGroup derivative. A logger
// using another slog.Handler may filter the record. The caller owns its message, severity and
// attributes.
func LogRequired(
	ctx context.Context,
	log *slog.Logger,
	level slog.Level,
	message string,
	attrs ...any,
) {
	log.Log(context.WithValue(ctx, requiredKey{}, true), level, message, attrs...)
}

func newLogger(output io.Writer, level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Time(slog.TimeKey, attr.Value.Time().UTC())
			}
			return attr
		},
	})
	return slog.New(&safeHandler{next: base})
}

type requiredKey struct{}

type safeHandler struct {
	next           slog.Handler
	sensitiveGroup bool
}

func (handler *safeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if ctx.Value(requiredKey{}) != nil {
		return true
	}
	return handler.next.Enabled(ctx, level)
}

func (handler *safeHandler) Handle(ctx context.Context, record slog.Record) error {
	safeRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		safeRecord.AddAttrs(handler.sanitize(attribute))
		return true
	})

	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		safeRecord.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	return handler.next.Handle(ctx, safeRecord)
}

func (handler *safeHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	safeAttributes := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		safeAttributes = append(safeAttributes, handler.sanitize(attribute))
	}
	return &safeHandler{
		next:           handler.next.WithAttrs(safeAttributes),
		sensitiveGroup: handler.sensitiveGroup,
	}
}

func (handler *safeHandler) WithGroup(name string) slog.Handler {
	return &safeHandler{
		next:           handler.next.WithGroup(name),
		sensitiveGroup: handler.sensitiveGroup || sensitiveKey(name),
	}
}

func (handler *safeHandler) sanitize(attribute slog.Attr) slog.Attr {
	if handler.sensitiveGroup {
		return slog.String(attribute.Key, redactedValue)
	}
	return sanitize(attribute)
}

func sanitize(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if sensitiveKey(attribute.Key) {
		return slog.String(attribute.Key, redactedValue)
	}
	if attribute.Value.Kind() == slog.KindAny {
		attribute.Value = sanitizeAny(attribute.Value)
		return attribute
	}
	if attribute.Value.Kind() != slog.KindGroup {
		return attribute
	}

	group := attribute.Value.Group()
	safeGroup := make([]slog.Attr, 0, len(group))
	for _, child := range group {
		safeGroup = append(safeGroup, sanitize(child))
	}
	return slog.Group(attribute.Key, attrsToAny(safeGroup)...)
}

func sanitizeAny(value slog.Value) slog.Value {
	data := value.Any()
	if _, ok := data.(error); ok {
		return slog.StringValue(redactedValue)
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		if isStructured(data) {
			return slog.StringValue(redactedValue)
		}
		return value
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		if isStructured(data) {
			return slog.StringValue(redactedValue)
		}
		return value
	}

	switch decoded.(type) {
	case map[string]any, []any:
		return slog.AnyValue(sanitizeStructured(decoded))
	default:
		return value
	}
}

func sanitizeStructured(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		safe := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey(key) {
				safe[key] = redactedValue
				continue
			}
			safe[key] = sanitizeStructured(child)
		}
		return safe
	case []any:
		safe := make([]any, len(typed))
		for index, child := range typed {
			safe[index] = sanitizeStructured(child)
		}
		return safe
	default:
		return value
	}
}

func isStructured(value any) bool {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() &&
		(reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return false
	}

	switch reflected.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.Struct:
		return true
	default:
		return false
	}
}

func attrsToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index := range attributes {
		values[index] = attributes[index]
	}
	return values
}

func sensitiveKey(key string) bool {
	lowerKey := strings.ToLower(key)
	words := strings.FieldsFunc(lowerKey, func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	for _, word := range words {
		switch word {
		case "authorization", "credential", "credentials", "password", "secret", "token":
			return true
		}
	}

	normalized := strings.Join(words, "_")
	switch normalized {
	case "request", "request_body", "request_payload", "response", "response_body",
		"response_payload", "customer", "customer_data", "policy", "policy_data", "payment",
		"payment_data":
		return true
	}

	compact := strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return character
		}
		return -1
	}, lowerKey)
	if strings.HasSuffix(compact, "token") || strings.Contains(compact, "password") ||
		strings.Contains(compact, "secret") || strings.Contains(compact, "credential") ||
		strings.Contains(
			compact,
			"authorization",
		) || strings.HasPrefix(compact, "customer") ||
		strings.HasPrefix(compact, "policy") || strings.HasPrefix(compact, "payment") {
		return true
	}
	switch compact {
	case "request", "requestbody", "requestpayload", "response", "responsebody",
		"responsepayload", "customer", "customerdata", "policy", "policydata", "payment",
		"paymentdata":
		return true
	default:
		return false
	}
}
