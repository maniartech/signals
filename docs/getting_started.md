# Getting Started

This guide will help you install, set up, and use the **signals** library in your Go project.

## Installation

```
go get github.com/maniartech/signals
```

## Basic Usage Example


```go
package main

import (
    "context"
    "fmt"
    "github.com/maniartech/signals"
)

func main() {
    sig := signals.New[string]()
    sig.AddListener(func(ctx context.Context, msg string) {
        fmt.Println("Received:", msg)
    })
    sig.Emit(context.Background(), "Hello, signals!")
}
```

## Running the Example

1. Save the code above in a file, e.g., `main.go`.
2. Run:
   ```
   go run main.go
   ```

---

- [Introduction](introduction.md)
- [Concepts](concepts.md)
- [API Reference](api_reference.md)
