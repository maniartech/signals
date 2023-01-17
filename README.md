# Signals

The `signals` a robust, dependency-free go library that provides simple, thin, and user-friendly pub-sub kind of in-process event system for your Go applications. It allows you to generate and emit signals (synchronously or asynchronously) as well as manage listeners.

💯 **100% test coverage** 💯

[![GoReportCard example](https://goreportcard.com/badge/github.com/nanomsg/mangos)](https://goreportcard.com/report/github.com/maniartech/signals)
[![<ManiarTech®️>](https://circleci.com/gh/maniartech/signals.svg?style=shield)](https://circleci.com/gh/maniartech/signals)</div>
[![made-with-Go](https://img.shields.io/badge/Made%20with-Go-1f425f.svg)](https://go.dev/)
[![GoDoc reference example](https://img.shields.io/badge/godoc-reference-blue.svg)](https://godoc.org/github.com/maniartech/signals)

## Installation

```bash
go get github.com/maniartech/signals
```

## Usage

```go
package main

import (
  "context"
  "fmt"
  "github.com/maniartech/signals"
)

var RecordCreated = signals.New[Record]()
var RecordUpdated = signals.New[Record]()
var RecordDeleted = signals.New[Record]()

func main() {

  // Add a listener to the RecordCreated signal
  RecordCreated.AddListener(func(ctx context.Context, record Record) {
    fmt.Println("Record created:", record)
  }, "key1") // <- Key is optional useful for removing the listener later

  // Add a listener to the RecordUpdated signal
  RecordUpdated.AddListener(func(ctx context.Context, record Record) {
    fmt.Println("Record updated:", record)
  })

  // Add a listener to the RecordDeleted signal
  RecordDeleted.AddListener(func(ctx context.Context, record Record) {
    fmt.Println("Record deleted:", record)
  })
  
  ctx := context.Background()

  // Emit the RecordCreated signal
  RecordCreated.Emit(ctx, Record{ID: 1, Name: "John"})

  // Emit the RecordUpdated signal
  RecordUpdated.Emit(ctx, Record{ID: 1, Name: "John Doe"})

  // Emit the RecordDeleted signal
  RecordDeleted.Emit(ctx, Record{ID: 1, Name: "John Doe"})
}
```

## Documentation

[![GoDoc](https://godoc.org/github.com/maniartech/signals?status.svg)](https://godoc.org/github.com/maniartech/signals)

## License

![License](https://img.shields.io/badge/license-MIT-blue.svg)

## ✨You Need Some Go Experts, Right? ✨

As a software development firm, ManiarTech® specializes in Golang-based projects. Our team has an in-depth understanding of Enterprise Process Automation, Open Source, and SaaS. Also, we have extensive experience porting code from Python and Node.js to Golang. We have a team of Golang experts here at ManiarTech® that is well-versed in all aspects of the language and its ecosystem.
At ManiarTech®, we have a team of Golang experts who are well-versed in all facets of the technology.

In short, if you're looking for experts to assist you with Golang-related projects, don't hesitate to get in touch with us. Send an email to contact@maniartech.com to get in touch.

## 👉🏼 Do you consider yourself an "Expert Golang Developer"? 👈🏼 ##

If so, you may be interested in the challenging and rewarding work that is waiting for you. Use careers@maniartech.com to submit your resume.
