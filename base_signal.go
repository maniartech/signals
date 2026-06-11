package signals

import (
	"context"
	"strconv"
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
	// auto indicates the key was synthesized internally (e.g. for an unkeyed
	// AddOnce so it can self-remove) rather than supplied by the caller. Such keys
	// are hidden from Keys() introspection.
	auto bool

	// listener is the standard callback function invoked when the signal is emitted
	listener SignalListener[T]

	// listenerErr is an error-returning listener variant used by TryEmit.
	// When present, it takes precedence over the standard listener.
	listenerErr SignalListenerErr[T]
}

// onceKeyPrefix namespaces internally generated keys for unkeyed AddOnce listeners.
// The NUL prefix makes a collision with a caller-supplied key effectively impossible.
const onceKeyPrefix = "\x00once-"

// onceCounter generates unique internal keys for unkeyed one-time listeners.
var onceCounter atomic.Uint64

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

	// onErr holds the per-signal error sinks registered via OnError. A non-nil error
	// returned by an AddListenerWithErr listener on the Emit path is routed to every
	// sink (on both sync and async signals). Stored as an immutable slice behind an
	// atomic pointer (copy-on-write registration under onErrMu) so routing is lock-free.
	onErr   atomic.Pointer[[]func(context.Context, error)]
	onErrMu sync.Mutex
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

	// MaxConcurrent bounds how many AsyncSignal handler goroutines may run at once
	// (a counting semaphore — there is no persistent worker pool and no Close()).
	//
	//   0 / unset → UNBOUNDED (a goroutine per handler, the safe default).
	//   > 0       → at most this many handlers execute concurrently; excess parks
	//               (cheaply) until a slot frees. Emit never blocks the caller; the
	//               parking happens in the background dispatcher.
	//   < 0       → treated as unset (unbounded).
	//
	// Bounding is an INFORMED OPT-IN: a bound can silently STARVE long-running
	// listeners (only MaxConcurrent run; the rest never start) — which is why the
	// default is unbounded. The bound caps concurrent *execution*, NOT the pending
	// backlog: under sustained overload, parked dispatch grows without a hard limit.
	// Size it to the slowest dependency (e.g. a DB connection limit), never to the
	// listener count. See signals.DefaultMaxConcurrent for a recommended value.
	//
	// Sync signals ignore this field. Has no effect once a signal is constructed.
	MaxConcurrent int
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
	kl := keyedListener[T]{listener: listener}
	// An empty-string key is treated as no key (unkeyed): per the FR-9 key contract,
	// the empty string is invisible to all key-based APIs (Keys/HasKey/RemoveListener).
	if len(key) > 0 && key[0] != "" {
		kl.key = key[0]
		kl.keyed = true
	}
	return s.add(kl)
}

// add appends a prebuilt keyedListener using copy-on-write under the write mutex.
// If the listener is keyed and its key already exists, it returns -1 and makes no
// change; otherwise it publishes a new slice and returns the new subscriber count.
func (s *BaseSignal[T]) add(kl keyedListener[T]) int {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if kl.keyed {
		if _, ok := s.subscribersMap[kl.key]; ok {
			return -1
		}
	}

	old := s.load()
	dup := s.cloneForWrite(old, 1)
	dup = append(dup, kl)
	if kl.keyed {
		s.subscribersMap[kl.key] = struct{}{}
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
	kl := keyedListener[T]{listenerErr: listener}
	// Empty-string key ⇒ treated as unkeyed (FR-9 key contract).
	if len(key) > 0 && key[0] != "" {
		kl.key = key[0]
		kl.keyed = true
	}
	return s.add(kl)
}

// AddOnce registers a listener that fires exactly once and then automatically
// removes itself. The one-shot guarantee is concurrency-safe: even if several
// emissions run simultaneously, the handler is invoked at most once.
//
// Panic policy: the one-shot is "consumed on attempt". The listener is marked fired
// and removed before the handler runs, so if the handler panics on its (only)
// invocation it is NOT retried on a later emission — the single attempt is spent.
// (On async signals the panic is recovered and routed to SetPanicHandler; on sync
// signals it propagates to the Emit caller, as with any sync listener.)
//
// An optional key makes the one-shot addressable (for early removal before it
// fires) and subject to duplicate detection: passing a key that already exists
// returns -1 and adds nothing. An absent or empty key is treated as unkeyed
// (FR-9 key contract).
//
// Returns the number of subscribers after adding the listener.
func (s *BaseSignal[T]) AddOnce(handler SignalListener[T], key ...string) int {
	k, userKeyed := oneShotUserKey(key)
	return s.addOnce(handler, k, userKeyed)
}

// AddOnceWithErr is the error-returning counterpart of AddOnce: the listener
// fires exactly once, removes itself, and reports failure via an error. It is to
// AddOnce what AddListenerWithErr is to AddListener, and accepts the same optional
// key. The returned error is routed the usual way — to OnError sinks on the Emit
// (best-effort) path, and collected into the joined result on TryEmit.
//
// The one-shot is "consumed on attempt" (see AddOnce): it fires and self-removes
// on its single invocation even if the handler returns a non-nil error; the error
// is reported but the listener is not retried on a later emission.
//
// Returns the number of subscribers after adding the listener.
func (s *BaseSignal[T]) AddOnceWithErr(handler SignalListenerErr[T], key ...string) int {
	if handler == nil {
		panic("listener cannot be nil")
	}
	k, auto := oneShotKey(oneShotUserKey(key))

	var fired atomic.Bool
	wrapper := func(ctx context.Context, payload T) error {
		if !fired.CompareAndSwap(false, true) {
			return nil // already fired by a concurrent emission
		}
		s.RemoveListener(k) // remove self first (see addOnce for the rationale)
		return handler(ctx, payload)
	}

	return s.add(keyedListener[T]{key: k, keyed: true, auto: auto, listenerErr: wrapper})
}

// oneShotUserKey normalizes the variadic key argument shared by AddOnce and
// AddOnceWithErr into (key, userKeyed). An absent or empty-string key is unkeyed.
func oneShotUserKey(key []string) (string, bool) {
	if len(key) > 0 && key[0] != "" {
		return key[0], true
	}
	return "", false
}

// oneShotKey resolves the actual listener key for a one-shot. A caller key is used
// as-is; otherwise (unkeyed, or empty per FR-9) an internal key is synthesized so
// the one-shot can self-remove — that key is hidden from Keys() and cannot collide
// with a caller key.
func oneShotKey(key string, userKeyed bool) (string, bool) {
	if userKeyed && key != "" {
		return key, false
	}
	return onceKeyPrefix + strconv.FormatUint(onceCounter.Add(1), 10), true
}

// addOnce wraps handler in a one-shot guard that runs it at most once (via an
// atomic compare-and-swap) and removes the listener after the first emission.
// Unkeyed one-time listeners get an internally generated key so they can
// self-remove; that key is hidden from Keys().
func (s *BaseSignal[T]) addOnce(handler SignalListener[T], key string, userKeyed bool) int {
	if handler == nil {
		panic("listener cannot be nil")
	}

	k, auto := oneShotKey(key, userKeyed)

	var fired atomic.Bool
	wrapper := func(ctx context.Context, payload T) {
		if !fired.CompareAndSwap(false, true) {
			return // already fired by a concurrent emission
		}
		// Remove self first so a re-entrant emit from within handler cannot
		// re-trigger this listener. Removing during emit is safe: the emit loop
		// iterates the previously published (immutable) slice.
		s.RemoveListener(k)
		handler(ctx, payload)
	}

	return s.add(keyedListener[T]{key: k, keyed: true, auto: auto, listener: wrapper})
}

// AddListenerWithCancel adds a repeating listener and returns a canceller func that
// removes it — the handle-based counterpart of AddListener for callers who do not
// want to invent and track a key. It is to AddListener what context.WithCancel is to
// a plain context: keep the returned func and call it to tear the subscription down
// (e.g. via defer), with no key bookkeeping.
//
// The optional key behaves exactly as in AddListener: a supplied key makes the
// listener addressable and subject to duplicate detection; an absent/empty key is
// unkeyed but still removable via the returned func (an internal key is synthesized
// so the canceller can target it, hidden from Keys()).
//
// The canceller is idempotent and safe: calling it more than once removes the
// listener at most once (so a later re-add of the same caller key is never removed by
// a stale canceller). If a supplied key already exists (duplicate), nothing is added
// and the returned func is a no-op — it will not remove the pre-existing listener.
// Like RemoveListener, cancellation affects subsequent emissions; an emission already
// in flight (which snapshotted this listener) may still deliver to it.
func (s *BaseSignal[T]) AddListenerWithCancel(handler SignalListener[T], key ...string) func() {
	if handler == nil {
		panic("listener cannot be nil")
	}
	// Reuse the one-shot key synthesizer purely as an internal auto-key generator for
	// the unkeyed case; a supplied key is used as-is.
	k, auto := oneShotKey(oneShotUserKey(key))
	if s.add(keyedListener[T]{key: k, keyed: true, auto: auto, listener: handler}) == -1 {
		return func() {} // duplicate caller key: do not remove someone else's listener
	}
	var once sync.Once
	return func() {
		once.Do(func() { s.RemoveListener(k) })
	}
}

// AddOnceWithCancel adds a one-shot listener (fires once, then self-removes) and
// returns a canceller func — the handle-based counterpart of AddOnce. Beyond the
// teardown convenience, the canceller serves a one-shot-specific purpose that AddOnce
// alone cannot: it removes a PENDING one-shot before it fires (e.g. when you give up
// waiting for an event that may never arrive), preventing an unkeyed one-shot from
// lingering in the listener set forever.
//
// The canceller is idempotent and races cleanly against the fire: it flips the same
// internal "fired" guard the one-shot uses, so a cancel that wins the race guarantees
// the handler will not run (stronger than a plain RemoveListener, which only affects
// subsequent emits). Calling it after the one-shot has already fired, or more than
// once, is a safe no-op. A duplicate caller key adds nothing and returns a no-op
// canceller. The optional key behaves as in AddOnce.
func (s *BaseSignal[T]) AddOnceWithCancel(handler SignalListener[T], key ...string) func() {
	if handler == nil {
		panic("listener cannot be nil")
	}
	k, auto := oneShotKey(oneShotUserKey(key))

	var fired atomic.Bool
	wrapper := func(ctx context.Context, payload T) {
		if !fired.CompareAndSwap(false, true) {
			return // already fired by a concurrent emission, or canceled
		}
		s.RemoveListener(k) // remove self first (see addOnce for the rationale)
		handler(ctx, payload)
	}

	if s.add(keyedListener[T]{key: k, keyed: true, auto: auto, listener: wrapper}) == -1 {
		return func() {} // duplicate caller key: no-op canceller
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// Win the race against an in-flight fire: claiming the guard first makes the
			// handler's CAS fail so it never runs; then drop it from the listener set.
			fired.CompareAndSwap(false, true)
			s.RemoveListener(k)
		})
	}
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

// Keys returns a snapshot of all caller-supplied listener keys. Internally
// generated keys (from unkeyed AddOnce) and empty-string keys are omitted. The
// snapshot is taken from the immutable published slice via a single atomic load,
// so it is safe to call while other goroutines add or remove listeners.
func (s *BaseSignal[T]) Keys() []string {
	subs := s.load()
	keys := make([]string, 0, len(subs))
	for i := range subs {
		if subs[i].keyed && !subs[i].auto && subs[i].key != "" {
			keys = append(keys, subs[i].key)
		}
	}
	return keys
}

// HasKey reports whether a listener with the given key is currently registered.
// It is an O(1) lookup. The check serializes with writers via the write mutex so
// it observes a consistent view of the keyed set.
func (s *BaseSignal[T]) HasKey(key string) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, ok := s.subscribersMap[key]
	return ok
}

// OnError registers a sink invoked when an error-returning listener (added via
// AddListenerWithErr) returns a non-nil error during Emit. Multiple sinks may be
// registered; all are invoked for every error. Sinks run on the goroutine that
// invoked the failing listener (the caller's for sync, a handler goroutine for
// async), so keep them cheap and non-blocking. A panic inside a sink is recovered
// (routed to SetPanicHandler) and does not stop the remaining sinks. OnError is safe
// for concurrent use. Errors on the TryEmit path are returned, not routed here.
func (s *BaseSignal[T]) OnError(sink func(ctx context.Context, err error)) {
	if sink == nil {
		return
	}
	s.onErrMu.Lock()
	defer s.onErrMu.Unlock()
	var next []func(context.Context, error)
	if cur := s.onErr.Load(); cur != nil {
		next = append(next, *cur...)
	}
	next = append(next, sink)
	s.onErr.Store(&next)
}

// routeError delivers err to every registered OnError sink. Each sink is isolated: a
// panicking sink is recovered (routed to SetPanicHandler) and does not prevent the
// remaining sinks from running.
func (s *BaseSignal[T]) routeError(ctx context.Context, err error) {
	p := s.onErr.Load()
	if p == nil {
		return
	}
	for _, sink := range *p {
		func() {
			defer func() {
				if r := recover(); r != nil {
					handleListenerPanic(r)
				}
			}()
			sink(ctx, err)
		}()
	}
}
