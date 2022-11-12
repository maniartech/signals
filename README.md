# Signals

Signals facilitate the development of simple, lightweight, and user-friendly event systems and simplified observer patterns in Go applications.
It allows you to generate and emit signals as well as manage listeners. The listeners are notified when the signal is emitted (synchronously or asynchronously).

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

  // Register a listener to the RecordCreated signal
  RecordCreated.Add(func(record Record) {
    fmt.Println("Record created:", record)
  }, "test")

  // Register a listener to the RecordUpdated signal
  RecordUpdated.Add(func(record Record) {
    fmt.Println("Record updated:", record)
  })

  // Register a listener to the RecordDeleted signal
  RecordDeleted.Add(func(record Record) {
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
