// Package gzap provides log handling using zap package.
// Code structure based on ginrus package.
// see github.com/gin-contrib/zap
package gzap

import (
	"bytes"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Option logger/recover option
type Option func(c *Config)

// WithCustomFields optional custom field
func WithCustomFields(fields ...func(c *gin.Context) zap.Field) Option {
	return func(c *Config) {
		c.customFields = fields
	}
}

// WithSkipLogging optional custom skip logging option.
func WithSkipLogging(f func(c *gin.Context) bool) Option {
	return func(c *Config) {
		if f != nil {
			c.skipLogging = f
		}
	}
}

// WithEnableBody optional custom enable request/response body.
func WithEnableBody(b bool) Option {
	return func(c *Config) {
		c.enableBody = b
	}
}

// WithBodyLimit optional custom request/response body limit.
// default: <=0, mean not limit
func WithBodyLimit(limit int) Option {
	return func(c *Config) {
		c.limit = limit
	}
}

// WithSkipRequestBody optional custom skip request body logging option.
func WithSkipRequestBody(f func(c *gin.Context) bool) Option {
	return func(c *Config) {
		if f != nil {
			c.skipRequestBody = f
		}
	}
}

// WithSkipResponseBody optional custom skip response body logging option.
func WithSkipResponseBody(f func(c *gin.Context) bool) Option {
	return func(c *Config) {
		if f != nil {
			c.skipResponseBody = f
		}
	}
}

// WithUseLoggerLevel optional use logging level.
func WithUseLoggerLevel(f func(c *gin.Context) zapcore.Level) Option {
	return func(c *Config) {
		if f != nil {
			c.useLoggerLevel = f
		}
	}
}

// WithFieldName optionally renames a log field.
// Example: `WithFieldName(gzap.FieldStatus, "httpStatusCode")`
func WithFieldName(index int, name string) Option {
	return func(c *Config) {
		if index > 0 && index < fieldMaxLen && name != "" {
			c.field[index] = name
		}
	}
}

// Indices for renaming field.
const (
	FieldStatus = iota
	FieldMethod
	FieldPath
	FieldRoute
	FieldQuery
	FieldIP
	FieldUserAgent
	FieldLatency
	FieldRequestBody
	FieldResponseBody
	fieldMaxLen
)

// Config logger/recover config
type Config struct {
	customFields []func(c *gin.Context) zap.Field
	// if returns true, it will skip logging.
	skipLogging func(c *gin.Context) bool
	// if returns true, it will skip request body.
	skipRequestBody func(c *gin.Context) bool
	// if returns true, it will skip response body.
	skipResponseBody func(c *gin.Context) bool
	// use logger level,
	// default:
	// 	zap.ErrorLevel: when status >= http.StatusInternalServerError && status <= http.StatusNetworkAuthenticationRequired
	// 	zap.WarnLevel: when status >= http.StatusBadRequest && status <= http.StatusUnavailableForLegalReasons
	//  zap.InfoLevel: otherwise.
	useLoggerLevel func(c *gin.Context) zapcore.Level
	enableBody     bool                // enable request/response body
	limit          int                 // <=0: mean not limit
	field          [fieldMaxLen]string // log field names
}

func skipRequestBody(c *gin.Context) bool {
	v := c.Request.Header.Get("Content-Type")
	d, params, err := mime.ParseMediaType(v)
	if err != nil || !(d == "multipart/form-data" || d == "multipart/mixed") {
		return false
	}
	_, ok := params["boundary"]
	return ok
}

func skipResponseBody(c *gin.Context) bool {
	// TODO: add skip response body rule
	return false
}

func useLoggerLevel(c *gin.Context) zapcore.Level {
	status := c.Writer.Status()
	if status >= http.StatusInternalServerError && status <= http.StatusNetworkAuthenticationRequired {
		return zap.ErrorLevel
	}
	if status >= http.StatusBadRequest && status <= http.StatusUnavailableForLegalReasons {
		return zap.WarnLevel
	}
	return zap.InfoLevel
}

func newConfig() Config {
	return Config{
		customFields:     nil,
		skipLogging:      func(c *gin.Context) bool { return false },
		skipRequestBody:  func(c *gin.Context) bool { return false },
		skipResponseBody: func(c *gin.Context) bool { return false },
		useLoggerLevel:   useLoggerLevel,
		enableBody:       false,
		limit:            0,
		field: [fieldMaxLen]string{
			"status",
			"method",
			"path",
			"route",
			"query",
			"ip",
			"user-agent",
			"latency",
			"requestBody",
			"responseBody",
		},
	}
}

// Logger returns a gin.HandlerFunc (middleware) that logs requests using uber-go/zap.
//
// Requests with errors are logged using zap.Error().
// Requests without errors are logged using zap.Info().
func Logger(logger *zap.Logger, opts ...Option) gin.HandlerFunc {
	cfg := newConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(c *gin.Context) {
		respBodyBuilder := &strings.Builder{}
		reqBody := "skip request body"

		if cfg.enableBody {
			c.Writer = &bodyWriter{ResponseWriter: c.Writer, dupBody: respBodyBuilder}
			if hasSkipRequestBody := skipRequestBody(c) || cfg.skipRequestBody(c); !hasSkipRequestBody {
				reqBodyBuf, err := io.ReadAll(c.Request.Body)
				if err != nil {
					c.String(http.StatusInternalServerError, err.Error())
					c.Abort()
					return
				}
				c.Request.Body.Close()
				c.Request.Body = io.NopCloser(bytes.NewBuffer(reqBodyBuf))
				if cfg.limit > 0 && len(reqBodyBuf) >= cfg.limit {
					reqBody = "larger request body"
				} else {
					reqBody = string(reqBodyBuf)
				}
			}
		}

		start := time.Now()
		// some evil middlewares modify this values
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		defer func() {
			if cfg.skipLogging(c) {
				return
			}
			var level zapcore.Level

			fieldLength := 8 + len(cfg.customFields) + 2
			if len(c.Errors) > 0 {
				level = zapcore.ErrorLevel
				fieldLength += len(c.Errors)
			} else {
				level = cfg.useLoggerLevel(c)
			}

			fields := make([]zap.Field, 0, fieldLength)
			fields = append(fields,
				zap.Int(cfg.field[FieldStatus], c.Writer.Status()),
				zap.String(cfg.field[FieldMethod], c.Request.Method),
				zap.String(cfg.field[FieldPath], path),
				zap.String(cfg.field[FieldRoute], c.FullPath()),
				zap.String(cfg.field[FieldQuery], query),
				zap.String(cfg.field[FieldIP], c.ClientIP()),
				zap.String(cfg.field[FieldUserAgent], c.Request.UserAgent()),
				zap.Duration(cfg.field[FieldLatency], time.Since(start)),
			)
			if cfg.enableBody {
				respBody := "skip response body"
				if hasSkipResponseBody := skipResponseBody(c) || cfg.skipResponseBody(c); !hasSkipResponseBody {
					if cfg.limit > 0 && respBodyBuilder.Len() >= cfg.limit {
						respBody = "larger response body"
					} else {
						respBody = respBodyBuilder.String()
					}
				}
				fields = append(fields,
					zap.String(cfg.field[FieldRequestBody], reqBody),
					zap.String(cfg.field[FieldResponseBody], respBody),
				)
			}
			for _, fieldFunc := range cfg.customFields {
				fields = append(fields, fieldFunc(c))
			}
			if len(c.Errors) > 0 {
				for _, e := range c.Errors {
					fields = append(fields, zap.Error(e))
				}
			}
			logger.Log(level, "logging", fields...)

		}()

		c.Next()
	}
}

// Recovery returns a gin.HandlerFunc (middleware)
// that recovers from any panics and logs requests using uber-go/zap.
// All errors are logged using zap.Error().
// stack means whether output the stack info.
// The stack info is easy to find where the error occurs but the stack info is too large.
func Recovery(logger *zap.Logger, stack bool, opts ...Option) gin.HandlerFunc {
	cfg := newConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if stack {
		cfg.customFields = append(cfg.customFields, func(c *gin.Context) zap.Field {
			return zap.ByteString("stack", debug.Stack())
		})
	}
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Check for a broken connection, as it is not really a
				// condition that warrants a panic stack trace.
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					if se, ok := ne.Err.(*os.SyscallError); ok {
						if strings.Contains(strings.ToLower(se.Error()), "broken pipe") ||
							strings.Contains(strings.ToLower(se.Error()), "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				httpRequest, _ := httputil.DumpRequest(c.Request, false)
				if brokenPipe {
					logger.Error(c.Request.URL.Path,
						zap.Any("error", err),
						zap.ByteString("request", httpRequest),
					)
					// If the connection is dead, we can't write a status to it.
					_ = c.Error(err.(error))
					c.Abort()
					return
				}

				fields := make([]zap.Field, 0, 2+len(cfg.customFields))
				fields = append(fields,
					zap.Any("error", err),
					zap.ByteString("request", httpRequest),
				)
				for _, field := range cfg.customFields {
					fields = append(fields, field(c))
				}
				logger.Error("recovery from panic", fields...)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

type bodyWriter struct {
	gin.ResponseWriter
	dupBody *strings.Builder
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	w.dupBody.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyWriter) WriteString(s string) (int, error) {
	w.dupBody.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// Any custom immutable any field
func Any(key string, value interface{}) func(c *gin.Context) zap.Field {
	field := zap.Any(key, value)
	return func(c *gin.Context) zap.Field { return field }
}

// String custom immutable string field
func String(key, value string) func(c *gin.Context) zap.Field {
	field := zap.String(key, value)
	return func(c *gin.Context) zap.Field { return field }
}

// Int64 custom immutable int64 field
func Int64(key string, value int64) func(c *gin.Context) zap.Field {
	field := zap.Int64(key, value)
	return func(c *gin.Context) zap.Field { return field }
}

// Uint64 custom immutable uint64 field
func Uint64(key string, value uint64) func(c *gin.Context) zap.Field {
	field := zap.Uint64(key, value)
	return func(c *gin.Context) zap.Field { return field }
}

// Float64 custom immutable float32 field
func Float64(key string, value float64) func(c *gin.Context) zap.Field {
	field := zap.Float64(key, value)
	return func(c *gin.Context) zap.Field { return field }
}
