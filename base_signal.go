package signals

import (
	"context"
	"sync"
)

// keyedListener represents a listener paired with an optional identification key.
// It can hold either a standard listener or an error-returning listener variant.
type keyedListener[T any] struct {
	// key is an optional unique identifier for the listener, allowing targeted removal
	key string
	// keyed indicates whether this listener was added with an explicit key
	keyed bool

	// listener is the standard callback function invoked when the signal is emitted
	listener SignalListener[T]

	// listenerErr is an error-returning listener variant used by TryEmit.
	// When present, it takes precedence over the standard listener.
	listenerErr SignalListenerErr[T]
}

// BaseSignal provides the foundational implementation for signal management.
// It handles listener registration, removal, and storage, but delegates the actual
// emission logic to derived types. This design allows for different emission strategies
// (synchronous, asynchronous) while sharing common listener management code.
//
// The BaseSignal uses an optimized storage strategy with both a slice for ordered
// iteration and a map for O(1) key lookups, ensuring efficient operations at scale.
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
	// mu protects concurrent access to subscribers
	mu sync.RWMutex

	// subscribers maintains the ordered list of registered listeners
	subscribers []keyedListener[T]

	// subscribersMap provides O(1) lookup for keyed listeners to prevent duplicates
	subscribersMap map[string]struct{}

	// growthFunc determines capacity allocation when the subscriber list needs to grow
	growthFunc func(currentCap int) int
}

// SignalOptions allows advanced users to customize memory allocation and growth behavior
// for the subscriber list. This is useful for optimizing performance when the expected
// number of listeners is known in advance, or when a specific growth pattern is desired.
//
// Both fields are optional; if not specified, sensible defaults based on prime numbers
// will be used to minimize memory fragmentation and optimize cache locality.
type SignalOptions struct {
	// InitialCapacity sets the initial capacity for the subscribers slice.
	// Default is 11 (a prime number for better hash distribution).
	InitialCapacity int

	// GrowthFunc determines the new capacity when the slice needs to grow.
	// It receives the current capacity and returns the desired new capacity.
	// Default uses a sequence of prime numbers for optimal performance.
	GrowthFunc func(currentCap int) int
}

// defaultInitialCapacity is the starting capacity for the subscribers slice.
// Using a prime number helps with cache locality and memory alignment.
var defaultInitialCapacity = 11

// defaultPrimes is a sequence of prime numbers used for capacity growth.
// Prime numbers help reduce memory fragmentation and optimize hash-based operations.
// The sequence covers capacities from small (11) to very large (2.1 billion).
var defaultPrimes = []int{11, 17, 23, 31, 47, 67, 97, 127, 197, 257, 389, 521, 769, 1031, 1543, 2053, 3079, 4099, 6151, 8209, 12289, 16381, 24593, 32771, 49157, 65537, 98317, 131071, 196613, 262147, 393241, 524287, 786433, 1048579, 1572869, 2097153, 3145739, 4194301, 6291469, 8388617, 12582917, 16777213, 25165843, 33554467, 50331653, 67108859, 100663319, 134217757, 201326611, 268435459, 402653189, 536870923, 805306457, 1073741827, 1610612741, 2147483647}

// defaultGrowthFunc implements the default capacity growth strategy using prime numbers.
// It searches for the next prime in the sequence that's larger than the current capacity.
// If the capacity exceeds the largest prime, it falls back to doubling plus one.
func defaultGrowthFunc(currentCap int) int {
	for _, p := range defaultPrimes {
		if p > currentCap {
			return p
		}
	}
	return currentCap*2 + 1 // fallback for extremely large capacities
}

// NewBaseSignal creates a new BaseSignal instance with customizable allocation behavior.
// Pass nil for opts to use the default configuration (initial capacity of 11, prime-based growth).
// This function is typically called by higher-level constructors like NewSync() or New().
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

func (s *BaseSignal[T]) ensureCapacity(extra int) {
	if s.growthFunc == nil {
		return
	}
	required := len(s.subscribers) + extra
	if required <= cap(s.subscribers) {
		return
	}
	newCap := s.growthFunc(cap(s.subscribers))
	if newCap < required {
		newCap = required
	}
	newSubs := make([]keyedListener[T], len(s.subscribers), newCap)
	copy(newSubs, s.subscribers)
	s.subscribers = newSubs
}

// AddListener registers a new listener that will be invoked when the signal is emitted.
// The listener will receive the context and payload on each emission.
//
// Parameters:
//   - listener: The callback function to invoke (must not be nil, will panic otherwise)
//   - key: Optional unique identifier for the listener
//
// Returns:
//   - The total number of subscribers after adding the listener
//   - Returns -1 if a keyed listener with the same key already exists (duplicate prevention)
//
// Keyed listeners enable targeted removal and prevent accidental duplicates.
// Listeners without keys cannot be individually removed later.
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
		s.ensureCapacity(1)
		s.subscribersMap[key[0]] = struct{}{}
		s.subscribers = append(s.subscribers, keyedListener[T]{
			key:      key[0],
			keyed:    true,
			listener: listener,
		})
	} else {
		s.ensureCapacity(1)
		s.subscribers = append(s.subscribers, keyedListener[T]{
			listener: listener,
		})
	}

	return len(s.subscribers)
}

// RemoveListener removes a listener identified by the given key from the signal.
// This method uses a swap-remove strategy for O(1) deletion, which may change
// the order of remaining listeners.
//
// Parameters:
//   - key: The unique identifier of the listener to remove
//
// Returns:
//   - The total number of subscribers remaining after removal
//   - Returns -1 if no listener with the given key was found
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
			if sub.keyed && sub.key == key {
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

// Reset removes all subscribers from the signal, effectively clearing the listener list.
// This operation is useful for cleanup scenarios, testing, or when you need to
// reconfigure all listeners from scratch.
//
// After calling Reset, the signal will have zero subscribers and no memory of
// previously registered listeners (including their keys).
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

// Emit is intentionally not implemented in BaseSignal and will panic if called directly.
// This method must be overridden by derived types (e.g., SyncSignal, AsyncSignal) to
// implement the specific emission strategy (synchronous vs asynchronous).
//
// Derived types should acquire a read lock, copy the subscriber list, and invoke
// each listener according to their execution model.
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

// Len returns the current number of registered subscribers.
// This method is safe for concurrent use.
func (s *BaseSignal[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers)
}

// IsEmpty returns true if the signal has no registered subscribers.
// This is a convenience method equivalent to checking if Len() == 0.
// This method is safe for concurrent use.
func (s *BaseSignal[T]) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers) == 0
}
