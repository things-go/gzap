// Package gzap provides log handling using zap package.
// Code structure based on ginrus package.
// see github.com/gin-contrib/zap
package gzap

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
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

// WithEnableBody optional custom request/response body limit.
// default: <=0, mean not limit
func WithBodyLimit(limit int) Option {
	return func(c *Config) {
		c.limit = limit
	}
}

// Config logger/recover config
type Config struct {
	customFields []func(c *gin.Context) zap.Field
	// if returns true, it will skip logging.
	skipLogging func(c *gin.Context) bool
	enableBody  bool // enable request/response body
	limit       int  // <=0: mean not limit
}

// Logger returns a gin.HandlerFunc (middleware) that logs requests using uber-go/zap.
//
// Requests with errors are logged using zap.Error().
// Requests without errors are logged using zap.Info().
func Logger(logger *zap.Logger, opts ...Option) gin.HandlerFunc {
	cfg := Config{
		nil,
		func(c *gin.Context) bool { return false },
		false,
		0,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(c *gin.Context) {
		respBody := &strings.Builder{}
		reqBody := ""

		if cfg.enableBody {
			c.Writer = &bodyWriter{ResponseWriter: c.Writer, dupBody: respBody}
			reqBodyBuf, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				c.Abort()
				return
			}
			c.Request.Body.Close()
			c.Request.Body = io.NopCloser(bytes.NewBuffer(reqBodyBuf))

			if cfg.limit > 0 && len(reqBodyBuf) >= cfg.limit {
				reqBody = "ignore larger req body"
			} else {
				reqBody = string(reqBodyBuf)
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

			if len(c.Errors) > 0 {
				// Append error field if this is an erroneous request.
				for _, e := range c.Errors.Errors() {
					logger.Error(e)
				}
			} else {
				fields := make([]zap.Field, 0, 8+len(cfg.customFields)+2)
				fields = append(fields,
					zap.Int("status", c.Writer.Status()),
					zap.String("method", c.Request.Method),
					zap.String("path", path),
					zap.String("route", c.FullPath()),
					zap.String("query", query),
					zap.String("ip", c.ClientIP()),
					zap.String("user-agent", c.Request.UserAgent()),
					zap.Duration("latency", time.Since(start)),
				)
				if cfg.enableBody {
					fields = append(fields, zap.String("requestBody", reqBody))
					if cfg.limit > 0 && respBody.Len() >= cfg.limit {
						fields = append(fields, zap.String("responseBody", "ignore larger response body"))
					} else {
						fields = append(fields, zap.String("responseBody", respBody.String()))
					}
				}
				for _, field := range cfg.customFields {
					fields = append(fields, field(c))
				}
				logger.Info("logging", fields...)
			}
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
	cfg := Config{
		nil,
		func(c *gin.Context) bool { return false },
		false,
		0,
	}
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
					c.Error(err.(error)) // nolint: errcheck
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

// Immutable custom immutable field
// Deprecated: use Any instead
func Immutable(key string, value interface{}) func(c *gin.Context) zap.Field {
	return Any(key, value)
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
