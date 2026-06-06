package signals

import (
	"context"
	"sync"
	"sync/atomic"
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
// # Concurrency model: lock-free reads, locked writes (copy-on-write)
//
// The subscriber list is stored as an *immutable* slice behind an atomic.Pointer.
// Readers (Emit/TryEmit/dispatch) perform a single atomic load and iterate the slice
// directly — no lock, no copy, no allocation on the hot path. Writers
// (Add/Remove/Reset) serialize on a write mutex, build a brand-new slice, and
// atomically swap it in. Because a published slice is never mutated again, a reader
// iterating an old slice is unaffected by a concurrent writer publishing a new one;
// the atomic pointer provides the necessary happens-before relationship. This is
// "lock-free reads, locked writes" copy-on-write — not a full CAS-loop algorithm.
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
	// writeMu serializes writers (AddListener/RemoveListener/Reset). Readers never
	// take this lock.
	writeMu sync.Mutex

	// subs holds the current immutable subscriber slice. Readers load it atomically
	// and iterate without copying; writers swap in a freshly built slice. A non-nil
	// pointer is installed by NewBaseSignal, but load() tolerates a nil (zero-value)
	// pointer and reports an empty list.
	subs atomic.Pointer[[]keyedListener[T]]

	// subscribersMap provides O(1) lookup for keyed listeners to prevent duplicates.
	// It is only read or mutated while holding writeMu, so it needs no separate
	// synchronization.
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
	s := &BaseSignal[T]{
		subscribersMap: make(map[string]struct{}),
		growthFunc:     growth,
	}
	initial := make([]keyedListener[T], 0, initCap)
	s.subs.Store(&initial)
	return s
}

// load returns the current immutable subscriber slice via a single atomic load.
// It is the read primitive for every emit path. A nil pointer (possible only on a
// zero-value BaseSignal that bypassed NewBaseSignal) reports an empty list.
func (s *BaseSignal[T]) load() []keyedListener[T] {
	if p := s.subs.Load(); p != nil {
		return *p
	}
	return nil
}

// cloneForWrite returns a fresh copy of the current subscriber slice with room for
// extra additional entries, honoring growthFunc when the capacity must grow. The
// caller must hold writeMu. Copy-on-write requires a new backing array on every
// write so the previously published slice is never mutated while readers iterate it.
func (s *BaseSignal[T]) cloneForWrite(old []keyedListener[T], extra int) []keyedListener[T] {
	required := len(old) + extra
	newCap := cap(old)
	if required > newCap {
		if s.growthFunc != nil {
			newCap = s.growthFunc(cap(old))
		}
		if newCap < required {
			newCap = required
		}
	}
	dup := make([]keyedListener[T], len(old), newCap)
	copy(dup, old)
	return dup
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
	if listener == nil {
		panic("listener cannot be nil")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var k string
	keyed := false
	if len(key) > 0 {
		k = key[0]
		keyed = true
		if _, ok := s.subscribersMap[k]; ok {
			return -1
		}
	}

	old := s.load()
	dup := s.cloneForWrite(old, 1)
	dup = append(dup, keyedListener[T]{key: k, keyed: keyed, listener: listener})
	if keyed {
		s.subscribersMap[k] = struct{}{}
	}
	s.subs.Store(&dup)
	return len(dup)
}

// AddListenerWithErr registers an error-returning listener that can report processing
// failures. These listeners are particularly useful with TryEmit(), which can detect
// and return errors.
//
// Parameters:
//   - listener: The error-returning callback function (must not be nil, will panic otherwise)
//   - key: Optional unique identifier for the listener
//
// Returns:
//   - The total number of subscribers after adding the listener
//   - Returns -1 if a keyed listener with the same key already exists
//
// Note: When both listener and listenerErr are set, listenerErr takes precedence during
// TryEmit().
func (s *BaseSignal[T]) AddListenerWithErr(listener SignalListenerErr[T], key ...string) int {
	if listener == nil {
		panic("listener cannot be nil")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var k string
	keyed := false
	if len(key) > 0 {
		k = key[0]
		keyed = true
		if _, ok := s.subscribersMap[k]; ok {
			return -1
		}
	}

	old := s.load()
	dup := s.cloneForWrite(old, 1)
	dup = append(dup, keyedListener[T]{key: k, keyed: keyed, listenerErr: listener})
	if keyed {
		s.subscribersMap[k] = struct{}{}
	}
	s.subs.Store(&dup)
	return len(dup)
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, ok := s.subscribersMap[key]; !ok {
		return -1
	}
	delete(s.subscribersMap, key)

	old := s.load()
	n := len(old)
	// Copy-on-write: build a fresh slice, then swap-remove within the copy so the
	// previously published slice (which readers may still be iterating) is untouched.
	dup := make([]keyedListener[T], n, cap(old))
	copy(dup, old)
	for i := range dup {
		if dup[i].keyed && dup[i].key == key {
			dup[i] = dup[n-1]
			dup = dup[:n-1]
			break
		}
	}
	s.subs.Store(&dup)
	return len(dup)
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	empty := make([]keyedListener[T], 0)
	s.subs.Store(&empty)
	s.subscribersMap = make(map[string]struct{})
}

// Emit is intentionally not implemented in BaseSignal and will panic if called directly.
// This method must be overridden by derived types (e.g., SyncSignal, AsyncSignal) to
// implement the specific emission strategy (synchronous vs asynchronous).
//
// Derived types should obtain the current subscriber slice with a single atomic load
// (see load) and iterate it directly according to their execution model.
func (s *BaseSignal[T]) Emit(ctx context.Context, payload T) {
	panic("implement me in derived type")
}

// Len returns the current number of registered subscribers.
// This method is safe for concurrent use and lock-free.
func (s *BaseSignal[T]) Len() int {
	return len(s.load())
}

// IsEmpty returns true if the signal has no registered subscribers.
// This is a convenience method equivalent to checking if Len() == 0.
// This method is safe for concurrent use and lock-free.
func (s *BaseSignal[T]) IsEmpty() bool {
	return len(s.load()) == 0
}
