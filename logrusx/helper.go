package logrusx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"

	"go.opentelemetry.io/otel/plugin/httptrace"

	"github.com/ory/x/errorsx"
)

type Logger struct {
	*logrus.Entry
	leakSensitive bool
}

func (l *Logger) Logrus() *logrus.Logger {
	return l.Entry.Logger
}

func (l *Logger) NewEntry() *Logger {
	ll := *l
	ll.Entry = logrus.NewEntry(l.Logger)
	return &ll
}

func (l *Logger) WithContext(ctx context.Context) *Logger {
	ll := *l
	ll.Entry = l.Logger.WithContext(ctx)
	return &ll
}

func (l *Logger) WithRequest(r *http.Request) *Logger {
	headers := map[string]interface{}{}
	if ua := r.UserAgent(); len(ua) > 0 {
		headers["user-agent"] = ua
	}

	if cookie := l.maybeRedact(r.Header.Get("Cookie")); cookie != nil {
		headers["cookie"] = cookie
	}

	if auth := l.maybeRedact(r.Header.Get("Authorization")); auth != nil {
		headers["authorization"] = auth
	}

	for _, key := range []string{"Referer", "Origin", "Accept", "X-Request-ID", "If-None-Match",
		"X-Forwarded-For", "X-Forwarded-Proto", "Cache-Control", "Accept-Encoding", "Accept-Language", "If-Modified-Since"} {
		if value := r.Header.Get(key); len(value) > 0 {
			headers[strings.ToLower(key)] = value
		}
	}

	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}

	ll := l.WithField("http_request", map[string]interface{}{
		"remote":  r.RemoteAddr,
		"method":  r.Method,
		"path":    r.URL.EscapedPath(),
		"query":   l.maybeRedact(r.URL.RawQuery),
		"scheme":  scheme,
		"host":    r.Host,
		"headers": headers,
	})

	if _, _, spanCtx := httptrace.Extract(r.Context(), r); spanCtx.IsValid() {
		traces := map[string]string{}
		if spanCtx.HasTraceID() {
			traces["trace_id"] = spanCtx.TraceID.String()
		}
		if spanCtx.HasSpanID() {
			traces["span_id"] = spanCtx.SpanID.String()
		}
		ll = ll.WithField("otel", traces)
	}

	return ll
}

func (l *Logger) WithFields(f logrus.Fields) *Logger {
	ll := *l
	ll.Entry = l.Entry.WithFields(f)
	return &ll
}

func (l *Logger) WithField(key string, value interface{}) *Logger {
	ll := *l
	ll.Entry = l.Entry.WithField(key, value)
	return &ll
}

func (l *Logger) maybeRedact(value interface{}) interface{} {
	if fmt.Sprintf("%v", value) == "" || value == nil {
		return nil
	}
	if !l.leakSensitive {
		return `Value is sensitive and has been redacted. To see the value set config key "log.leak_sensitive_values = true" or environment variable "LOG_LEAK_SENSITIVE_VALUES=true".`
	}
	return value
}

func (l *Logger) WithSensitiveField(key string, value interface{}) *Logger {
	return l.WithField(key, l.maybeRedact(value))
}

func (l *Logger) WithError(err error) *Logger {
	ctx := map[string]interface{}{"message": err.Error()}
	if l.Entry.Logger.IsLevelEnabled(logrus.TraceLevel) {
		if e, ok := err.(errorsx.StackTracer); ok {
			ctx["trace"] = fmt.Sprintf("%+v", e.StackTrace())
		} else {
			ctx["trace"] = fmt.Sprintf("stack trace could not be recovered from error type %s", reflect.TypeOf(err))
		}
	}
	if c := errorsx.ReasonCarrier(nil); errors.As(err, &c) {
		ctx["reason"] = c.Reason()
	}
	if c := errorsx.RequestIDCarrier(nil); errors.As(err, &c) && c.RequestID() != "" {
		ctx["request_id"] = c.RequestID()
	}
	if c := errorsx.DetailsCarrier(nil); errors.As(err, &c) && c.Details() != nil {
		ctx["details"] = c.Details()
	}
	if c := errorsx.StatusCarrier(nil); errors.As(err, &c) && c.Status() != "" {
		ctx["status"] = c.Status()
	}
	if c := errorsx.StatusCodeCarrier(nil); errors.As(err, &c) && c.StatusCode() != 0 {
		ctx["status_code"] = c.StatusCode()
	}
	if c := errorsx.DebugCarrier(nil); errors.As(err, &c) {
		ctx["debug"] = c.Debug()
	}

	return l.WithField("error", ctx)
}
