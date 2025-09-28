# Signals Library - Future Features & Enhancements

This document outlines potential features and improvements planned for future versions of the Signals library.

## v1.4.0 - Planned Features

### **AsyncSignal Error Handling Support**

#### **Problem Statement**
Currently, only `SyncSignal` supports `AddListenerWithErr` for error-handling workflows. `AsyncSignal` is designed for fire-and-forget operations, but there are scenarios where users still want to handle errors from async listeners (logging, monitoring, retry logic, etc.).

#### **Proposed Solution: Simple Error Handler Pattern**

Add `AddListenerWithErr` support to `AsyncSignal` using a simple error handler callback pattern focused on logging and reporting errors. The async nature is preserved - errors don't affect the flow of other listeners.

##### **Implementation Approach**

```go
type AsyncSignal[T any] struct {
    baseSignal     *BaseSignal[T]
    workerPoolOnce sync.Once
    workerPool     chan *emitTask[T]
    poolSize       int
    errorHandler   func(error, T, string) // error, payload, listener key
    errorMu        sync.RWMutex
}

// SetErrorHandler sets a global error handler for this signal
// Used for logging, monitoring, alerting - does not affect processing flow
func (s *AsyncSignal[T]) SetErrorHandler(handler func(error, T, string)) {
    s.errorMu.Lock()
    defer s.errorMu.Unlock()
    s.errorHandler = handler
}

// AddListenerWithErr adds an error-returning listener to async signal
func (s *AsyncSignal[T]) AddListenerWithErr(listener SignalListenerErr[T], key ...string) int {
    listenerKey := ""
    if len(key) > 0 {
        listenerKey = key[0]
    }

    wrappedListener := func(ctx context.Context, payload T) {
        if err := listener(ctx, payload); err != nil {
            s.errorMu.RLock()
            handler := s.errorHandler
            s.errorMu.RUnlock()

            if handler != nil {
                // Handle error asynchronously - never blocks other listeners
                go handler(err, payload, listenerKey)
            }
        }
    }
    return s.baseSignal.AddListener(wrappedListener, key...)
}
```

##### **Usage Examples**

**Example 1: Error Logging**
```go
asyncSig := signals.New[UserData]()

// Simple error logging
asyncSig.SetErrorHandler(func(err error, user UserData, key string) {
    log.Printf("Listener [%s] error for user %d: %v", key, user.ID, err)
})

asyncSig.AddListenerWithErr(func(ctx context.Context, user UserData) error {
    return sendEmail(user) // Might fail, but other listeners continue
}, "email-sender")

asyncSig.AddListenerWithErr(func(ctx context.Context, user UserData) error {
    return updateAnalytics(user) // Independent of email success/failure
}, "analytics")

// All listeners run regardless of individual failures
asyncSig.Emit(ctx, userData)
```

**Example 2: Error Monitoring & Alerting**
```go
asyncSig := signals.New[OrderData]()

// Error monitoring with metrics and alerting
asyncSig.SetErrorHandler(func(err error, order OrderData, key string) {
    // Log the error
    logger.Error("Order processing error",
        "listener", key,
        "order_id", order.ID,
        "error", err)

    // Update metrics
    metrics.IncrementCounter("order_processing_errors", map[string]string{
        "listener": key,
        "error_type": getErrorType(err),
    })

    // Send alerts for critical listeners
    if key == "payment-processor" || key == "inventory-updater" {
        alerting.SendAlert("Critical order processing error", err)
    }
})

asyncSig.AddListenerWithErr(processPayment, "payment-processor")
asyncSig.AddListenerWithErr(updateInventory, "inventory-updater")
asyncSig.AddListenerWithErr(sendNotifications, "notifications")
```

**Example 3: Error Retry Logic**
```go
asyncSig := signals.New[EventData]()

// Error handler with retry logic
asyncSig.SetErrorHandler(func(err error, event EventData, key string) {
    if isRetryableError(err) {
        // Add to retry queue
        retryQueue.Add(RetryTask{
            ListenerKey: key,
            Payload: event,
            Error: err,
            Attempts: 0,
        })
        log.Info("Added failed task to retry queue", "listener", key, "error", err)
    } else {
        log.Error("Non-retryable error", "listener", key, "error", err)
    }
})
```

##### **Benefits**
- ✅ **Maintains async nature** - Errors never block other listeners
- ✅ **Simple API** - Single error handler function, easy to understand
- ✅ **Flexible error handling** - Log, monitor, alert, retry as needed
- ✅ **Zero performance impact** - Error handling is always async
- ✅ **Fire-and-forget preserved** - Core async philosophy maintained
- ✅ **Easy integration** - Works with existing logging/monitoring systems

##### **Why This Approach Makes Sense**

- **🎯 Focuses on real use cases**: Logging, monitoring, alerting, retry logic
- **🚀 Keeps it simple**: No complex flow control or state management
- **⚡ Performance first**: Never blocks or affects other listeners
- **📊 Observable**: Perfect for metrics, monitoring, and debugging
- **🔧 Practical**: Solves actual problems developers face with async operations
- ✅ **Non-blocking by default** - Doesn't affect emit performance
- ✅ **Context-aware decisions** - Handler has access to payload and listener key
- ✅ **Backward compatible** - Can default to continue behavior
- ✅ **Performance optimized** - Stop processing when needed to save resources

##### **Implementation Challenges & Solutions**

**Challenge 1: Coordinating Stop Signal Across Goroutines**
```go
// Use atomic flag and context cancellation
type emitContext[T any] struct {
    ctx      context.Context
    cancel   context.CancelFunc
    stopFlag *atomic.Bool
}

// Each listener goroutine checks stop flag before processing
func (s *AsyncSignal[T]) workerRoutine(task *emitTask[T]) {
    // Check if we should stop before processing
    if task.stopFlag.Load() {
        return // Skip processing
    }

    // Process listener...
}
```

**Challenge 2: Ensuring Proper Cleanup on Early Stop**
```go
// Modified emit with proper cleanup
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
    // ... setup code ...

    emitCtx, cancel := context.WithCancel(ctx)
    defer cancel()

    stopFlag := &atomic.Bool{}

    // If error handler returns false, cancel context and set stop flag
    errorWrapper := func(err error, payload T, key string) {
        shouldContinue := s.errorHandler(err, payload, key)
        if !shouldContinue {
            stopFlag.Store(true)
            cancel() // Cancel context to stop remaining listeners
        }
    }

    // ... emit logic with stop flag checks ...
}
```

##### **Advanced Usage: Error Classification**
```go
type ErrorSeverity int

const (
    ErrorSeverityLow ErrorSeverity = iota
    ErrorSeverityMedium
    ErrorSeverityHigh
    ErrorSeverityCritical
)

func classifyError(err error) ErrorSeverity {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return ErrorSeverityMedium
    case errors.Is(err, syscall.ECONNREFUSED):
        return ErrorSeverityHigh
    case strings.Contains(err.Error(), "database"):
        return ErrorSeverityCritical
    default:
        return ErrorSeverityLow
    }
}
```

##### **Alternative Approaches Considered**
1. **Error Channel Pattern** - Provide error channel for users to read from
2. **Logging/Metrics Pattern** - Built-in logging with configurable loggers
3. **Promise/Future Pattern** - Return result objects with error collection

**Recommendation**: Error Handler Pattern provides the best balance of flexibility and performance.

## Other Potential Features for Future Versions

### **v1.4.0+ - Additional Features**

#### **Generic Signal Aggregation**
- Multi-signal coordination patterns
- Wait for multiple signals to complete
- Signal combination and transformation

#### **Metrics & Observability**
- Built-in performance monitoring
- Emit duration tracking
- Listener execution statistics
- Error rate monitoring

#### **Signal Pipelines**
- Chainable signal transformations
- Signal composition patterns
- Pipeline builder API

#### **Custom Allocation Strategies**
- Even more memory optimization options
- Custom pool implementations
- Memory-constrained environments support

#### **Signal Persistence**
- Optional event sourcing capabilities
- Signal replay functionality
- Persistent listener state

---

**Note**: These features are under consideration and may change based on community feedback and real-world usage patterns.
