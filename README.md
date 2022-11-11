# Signals

Signals provides a simple, lightweight, and easy to use events library for Go.
It allows you to create a signal, add listeners to it, and emit events
to the signal. The listeners will be called when the signal is emitted.
The listeners can be added and removed at any time. Signals
can be emitted asynchonously or synchronously. Signals can be used to create
event systems, or to create a simple observer pattern.

Anyone can register a listener to a signal. The signal does not need to know
who is listening to it. Anyone can emit a signal. The signal does not need to
know who is emitting it.

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

var RecordCreated = signals.New[Record]()
var RecordUpdated = signals.New[Record]()
var RecordDeleted = signals.New[Record]()

func main() {

  // Register a listener to the RecordCreated signal
  RecordCreated(func(record Record) {
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

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](
