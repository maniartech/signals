package signals

// NewSync createa a new signal that can be used to emit and listen to events
// synchronously.
// example:
//  package main
//
//  import (
// 	  "fmt"
//
// 	  "github.com/maniartech/signals"
//  )
//
//  func main() {
//  	s := signals.NewSync[int]()
// 	  s.AddListener(func(ctx context.Context, v int) {
// 		  fmt.Println(v)
// 	  })
// 	  s.Emit(1)
//  }
//
func NewSync[T any]() Signal[T] {
	s := &SyncSignal[T]{}
	s.Reset()
	return s
}

// New creates a new signal that can be used to emit and listen to events
// asynchronously. When emitting an event, the signal will call all the
// listeners in a separate goroutine. This is useful when you want to emit
// signals but don't want to block the current goroutine. It does not guarantee
// the type safety of the emitted value.
// example:
//
// package main
//
//  import (
// 	  "fmt"
//    "github.com/maniartech/signals"
//  )
//
//  func main() {
//    s := signals.New[int]()
// 	  s.AddListener(func(ctx context.Context, v int) {
// 		  fmt.Println(v)
// 	  })
// 	  s.Emit(1)
//  }
//
func New[T any]() Signal[T] {
	s := &AsyncSignal[T]{}
	s.Reset() // Reset the signal
	return s
}
