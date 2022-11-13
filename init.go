package signals

// New createa a new signal that can be used to emit and listen to events
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
//  	s := signals.New[int]()
// 	  s.Add(func(v int) {
// 		  fmt.Println(v)
// 	  })
// 	  s.Emit(1)
//  }
//
func New[T any]() Signal[T] {

	s := &signal[T]{}
	s.Reset()
	return s
}

// NewAsync creates a new signal that can be used to emit and listen to events
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
//    s := signals.NewAsync[int]()
// 	  s.Add(func(v int) {
// 		  fmt.Println(v)
// 	  })
// 	  s.Emit(1)
//  }
//
func NewAsync[T any]() Signal[T] {
	s := signal[T]{
		async: true,
	}
	s.Reset() // Reset the signal
	return &s
}
