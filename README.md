# gzap

gzap is a zap middleware for [Gin](https://github.com/gin-gonic/gin)  

[![GoDoc](https://godoc.org/github.com/things-go/gzap?status.svg)](https://godoc.org/github.com/things-go/gzap)
[![Go.Dev reference](https://img.shields.io/badge/go.dev-reference-blue?logo=go&logoColor=white)](https://pkg.go.dev/github.com/things-go/gzap?tab=doc)
[![codecov](https://codecov.io/gh/things-go/gzap/branch/master/graph/badge.svg)](https://codecov.io/gh/things-go/gzap)
![Action Status](https://github.com/things-go/gzap/workflows/Go/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/things-go/gzap)](https://goreportcard.com/report/github.com/things-go/gzap)
[![Licence](https://img.shields.io/github/license/things-go/gzap)](https://raw.githubusercontent.com/things-go/gzap/master/LICENSE)
[![Tag](https://img.shields.io/github/v/tag/things-go/gzap)](https://github.com/thinkgos/requestid/tags)


## Usage

### Installation

Use go get.
```bash
    go get github.com/things-go/gzap
```

Then import the gazp package into your own code.
```go
    import "github.com/things-go/gzap"
```

### Example

[embedmd]:# (_example/main.go go)
```go
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/things-go/gzap"
)

func main() {
	r := gin.New()

	logger, _ := zap.NewProduction()

	// Add a ginzap middleware, which:
	//   - Logs all requests, like a combined access and error log.
	//   - Logs to stdout.
	//   - RFC3339 with UTC time format.
	r.Use(gzap.Logger(logger,
		gzap.WithCustomFields(
			gzap.String("app", "example"),
			func(c *gin.Context) zap.Field { return zap.String("custom field1", c.ClientIP()) },
			func(c *gin.Context) zap.Field { return zap.String("custom field2", c.ClientIP()) },
		),
		gzap.WithSkipLogging(func(c *gin.Context) bool {
			return c.Request.URL.Path == "/skiplogging"
		}),
		gzap.WithEnableBody(),
	))

	// Logs all panic to error log
	//   - stack means whether output the stack info.
	r.Use(gzap.Recovery(logger, true,
		gzap.WithCustomFields(
			gzap.Immutable("app", "example"),
			func(c *gin.Context) zap.Field { return zap.String("custom field1", c.ClientIP()) },
			func(c *gin.Context) zap.Field { return zap.String("custom field2", c.ClientIP()) },
		),
	))

	// Example ping request.
	r.GET("/ping", func(c *gin.Context) {
		c.String(200, "pong "+fmt.Sprint(time.Now().Unix()))
	})

	// Example when panic happen.
	r.GET("/panic", func(c *gin.Context) {
		panic("An unexpected error happen!")
	})

	r.GET("/error", func(c *gin.Context) {
		c.Error(errors.New("An error happen 1")) // nolint: errcheck
		c.Error(errors.New("An error happen 2")) // nolint: errcheck
	})

	r.GET("/skiplogging", func(c *gin.Context) {
		c.String(200, "i am skip logging, log should be not output")
	})

	// Listen and Server in 0.0.0.0:8080
	r.Run(":8080")
}
```

## License

This project is under MIT License. See the [LICENSE](LICENSE) file for the full license text.
