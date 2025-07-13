# Signals

The `signals` a robust, dependency-free go library that provides simple, thin, and user-friendly pub-sub kind of in-process event system for your Go applications. It allows you to generate and emit signals (synchronously or asynchronously) as well as manage listeners.

💯 **100% test coverage** 💯

⚠️ **Note:** This project is stable, production-ready and complete. It is used in production by [ManiarTech®️](https://maniartech.com) and other companies. Hence, we won't be adding any new features to this project. However, we will continue to maintain it by fixing bugs and keeping it up-to-date with the latest Go versions. We shall however, be adding new features when the need arises and / or requested by the community.

[![GoReportCard example](https://goreportcard.com/badge/github.com/nanomsg/mangos)](https://goreportcard.com/report/github.com/maniartech/signals)
[![<ManiarTech®️>](https://circleci.com/gh/maniartech/signals.svg?style=shield)](https://circleci.com/gh/maniartech/signals)
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


## Benchmarks


### Benchmark Philosophy

These benchmarks are designed to measure the real-world performance and scalability of the signals library under a variety of usage patterns. Each benchmark targets a specific aspect of signal/event system performance that is critical for robust, production-grade applications:

- **Signal Emit (Single Listener):** Measures the minimal overhead of emitting a signal to a single listener. This reflects the best-case latency for the most common use case—fast, in-process event notification.
- **Signal Emit (Many Listeners, 100):** Tests the cost of emitting to a large number of listeners. This simulates scenarios where many components need to react to the same event, and demonstrates how the library scales with listener count.
- **Signal Emit (Concurrent):** Evaluates the library's ability to handle extremely high-frequency, concurrent signal emissions from many goroutines. This is crucial for high-throughput, parallel workloads and validates thread safety under stress.
- **Add/Remove Listener (Concurrent):** Measures the performance of dynamically adding and removing listeners while signals are being emitted. This is important for systems where listeners are frequently registered/unregistered at runtime, and tests the efficiency and safety of listener management.

Together, these benchmarks provide a comprehensive view of both the raw speed and the concurrency characteristics of the signals package. They ensure the library is suitable for demanding, high-concurrency, and low-latency applications.

The following benchmarks were run on an AMD Ryzen 7 5700G (Windows, Go 1.XX):

| Benchmark                                 | Iterations   | Time per op (ns) |
|--------------------------------------------|--------------|------------------|
| Signal Emit (Single Listener)              |  19,319,023  |          618.3   |
| Signal Emit (Many Listeners, 100)          |     532,440  |       22,903.0   |
| Signal Emit (Concurrent)                   | 100,000,000  |          107.5   |
| Add/Remove Listener (Concurrent)           |  84,484,135  |          134.8   |

These results demonstrate high performance and scalability under both sequential and concurrent workloads. The signals package is suitable for demanding, high-concurrency applications.

## Documentation

[![GoDoc](https://godoc.org/github.com/maniartech/signals?status.svg)](https://godoc.org/github.com/maniartech/signals)

## License

![License](https://img.shields.io/badge/license-MIT-blue.svg)

## ✨You Need Some Go Experts, Right? ✨

As a software development firm, ManiarTech® specializes in Golang-based projects. Our team has an in-depth understanding of Enterprise Process Automation, Open Source, and SaaS. Also, we have extensive experience porting code from Python and Node.js to Golang. We have a team of Golang experts here at ManiarTech® that is well-versed in all aspects of the language and its ecosystem.
At ManiarTech®, we have a team of Golang experts who are well-versed in all facets of the technology.

In short, if you're looking for experts to assist you with Golang-related projects, don't hesitate to get in touch with us. Send an email to <contact@maniartech.com> to get in touch.

## 👉🏼 Do you consider yourself an "Expert Golang Developer"? 👈🏼

If so, you may be interested in the challenging and rewarding work that is waiting for you. Use <careers@maniartech.com> to submit your resume.
