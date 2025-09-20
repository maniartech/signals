package signals

import (
	"context"
	"sync"
)

// keyedListener represents a combination of a listener and an optional key used for identification.
type keyedListener[T any] struct {
	key      string
	listener SignalListener[T]

	// listenerErr is an optional listener variant that can return an error.
	// Used by TryEmit
	listenerErr SignalListenerErr[T]
}

// BaseSignal provides the base implementation of the Signal interface.
// It is intended to be used as an abstract base for underlying signal mechanisms.
//
// Example:
//
//	type MyDerivedSignal[T any] struct {
//		BaseSignal[T]
//		// Additional fields or methods specific to MyDerivedSignal
//	}
//
//	func (s *MyDerivedSignal[T]) Emit(ctx context.Context, payload T) {
//		// Custom implementation for emitting the signal
//	}
type BaseSignal[T any] struct {
	mu             sync.RWMutex
	subscribers    []keyedListener[T]
	subscribersMap map[string]struct{}
	growthFunc     func(currentCap int) int
}

// SignalOptions allows advanced users to configure allocation and growth strategy for listeners.
type SignalOptions struct {
	InitialCapacity int
	GrowthFunc      func(currentCap int) int
}

// Default values for backward compatibility
var defaultInitialCapacity = 11
var defaultPrimes = []int{11, 17, 23, 31, 47, 67, 97, 127, 197, 257, 389, 521, 769, 1031, 1543, 2053, 3079, 4099, 6151, 8209, 12289, 16381, 24593, 32771, 49157, 65537, 98317, 131071, 196613, 262147, 393241, 524287, 786433, 1048579, 1572869, 2097153, 3145739, 4194301, 6291469, 8388617, 12582917, 16777213, 25165843, 33554467, 50331653, 67108859, 100663319, 134217757, 201326611, 268435459, 402653189, 536870923, 805306457, 1073741827, 1610612741, 2147483647}

func defaultGrowthFunc(currentCap int) int {
	for _, p := range defaultPrimes {
		if p > currentCap {
			return p
		}
	}
	return currentCap*2 + 1 // fallback
}

// NewBaseSignal creates a BaseSignal with optional allocation/growth options.
func NewBaseSignal[T any](opts *SignalOptions) *BaseSignal[T] {
	initCap := defaultInitialCapacity
	growth := defaultGrowthFunc
	if opts != nil {
		if opts.InitialCapacity > 0 {
			initCap = opts.InitialCapacity
		}
		if opts.GrowthFunc != nil {
			growth = opts.GrowthFunc
		}
	}
	return &BaseSignal[T]{
		subscribers:    make([]keyedListener[T], 0, initCap),
		subscribersMap: make(map[string]struct{}),
		growthFunc:     growth,
	}
}

// AddListener adds a listener to the signal. The listener will be called
// whenever the signal is emitted. It returns the number of subscribers after
// the listener was added. It accepts an optional key that can be used to remove
// the listener later or to check if the listener was already added. It returns
// -1 if the listener with the same key was already added to the signal.
//
// Example:
//
//	signal := signals.New[int]()
//	count := signal.AddListener(func(ctx context.Context, payload int) {
//		// Listener implementation
//		// ...
//	}, "key1")
//	fmt.Println("Number of subscribers after adding listener:", count)
func (s *BaseSignal[T]) AddListener(listener SignalListener[T], key ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if listener == nil {
		panic("listener cannot be nil")
	}

	if len(key) > 0 {
		if _, ok := s.subscribersMap[key[0]]; ok {
			return -1
		}
		s.subscribersMap[key[0]] = struct{}{}
		s.subscribers = append(s.subscribers, keyedListener[T]{
			key:      key[0],
			listener: listener,
		})
	} else {
		s.subscribers = append(s.subscribers, keyedListener[T]{
			listener: listener,
		})
	}

	return len(s.subscribers)
}

// RemoveListener removes a listener from the signal. It returns the number
// of subscribers after the listener was removed. It returns -1 if the
// listener was not found.
//
// Example:
//
//	signal := signals.New[int]()
//	signal.AddListener(func(ctx context.Context, payload int) {
//		// Listener implementation
//		// ...
//	}, "key1")
//	count := signal.RemoveListener("key1")
//	fmt.Println("Number of subscribers after removing listener:", count)
func (s *BaseSignal[T]) RemoveListener(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subscribersMap[key]; ok {
		delete(s.subscribersMap, key)
		n := len(s.subscribers)
		for i, sub := range s.subscribers {
			if sub.key == key {
				// Swap with last and remove last (swap-remove, avoids allocation)
				s.subscribers[i] = s.subscribers[n-1]
				s.subscribers = s.subscribers[:n-1]
				break
			}
		}
		return len(s.subscribers)
	}
	return -1
}

// Reset resets the signal by removing all subscribers from the signal,
// effectively clearing the list of subscribers.
// This can be used when you want to stop all listeners from receiving
// further signals.
//
// Example:
//
//	signal := signals.New[int]()
//	signal.AddListener(func(ctx context.Context, payload int) {
//		// Listener implementation
//		// ...
//	})
//	signal.Reset() // Removes all listeners
//	fmt.Println("Number of subscribers after resetting:", signal.Len())
func (s *BaseSignal[T]) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.subscribers = make([]keyedListener[T], 0)
	s.subscribersMap = make(map[string]struct{})
}

// Emit is not implemented in BaseSignal and panics if called. It should be
// implemented by a derived type.
//
// Example:
//
//	type MyDerivedSignal[T any] struct {
//		BaseSignal[T]
//		// Additional fields or methods specific to MyDerivedSignal
//	}
//
//	func (s *MyDerivedSignal[T]) Emit(ctx context.Context, payload T) {
//		// Custom implementation for emitting the signal
//	}
func (s *BaseSignal[T]) Emit(ctx context.Context, payload T) {
	panic("implement me in derived type")
}

func (s *BaseSignal[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers)
}

func (s *BaseSignal[T]) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers) == 0
}
