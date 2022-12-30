# Signals

The `signals` a robust, dependency-free go library that provides simple, thin, and user-friendly pub-sub kind of in-process event system for your Go applications. It allows you to generate and emit signals (synchronously or asynchronously) as well as manage listeners.

💯 **100% test coverage** 💯

## Installation

```bash
go get github.com/maniartech/signals
```

## Usage

```go
package main

import (
  "fmt"
  "github.com/maniartech/signals"
)

var RecordCreated = signals.NewAsync[Record]()
var RecordUpdated = signals.NewAsync[Record]()
var RecordDeleted = signals.NewAsync[Record]()

func main() {

  // Add a listener to the RecordCreated signal
  RecordCreated.AddListener(func(record Record) {
    fmt.Println("Record created:", record)
  }, "key1") // <- Key is optional useful for removing the listener later

  // Add a listener to the RecordUpdated signal
  RecordUpdated.AddListener(func(record Record) {
    fmt.Println("Record updated:", record)
  })

  // Add a listener to the RecordDeleted signal
  RecordDeleted.AddListener(func(record Record) {
    fmt.Println("Record deleted:", record)
  })

  // Emit the RecordCreated signal
  RecordCreated.Emit(Record{ID: 1, Name: "John"})

  // Emit the RecordUpdated signal
  RecordUpdated.Emit(Record{ID: 1, Name: "John Doe"})

  // Emit the RecordDeleted signal
  RecordDeleted.Emit(Record{ID: 1, Name: "John Doe"})
}
```

## Documentation

[![GoDoc](https://godoc.org/github.com/maniartech/signals?status.svg)](https://godoc.org/github.com/maniartech/signals)

## License

![License](https://img.shields.io/badge/license-MIT-blue.svg)
