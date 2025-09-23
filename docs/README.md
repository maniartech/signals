# Signals Documentation

**High-Performance, Production-Ready In-Process Event System for Go**

Welcome to the complete documentation for the Signals library - a sub-10 nanosecond, zero-allocation event system designed for mission-critical Go applications.

## Quick Navigation

### [Introduction](./introduction.md)
Start here to understand what Signals is and see performance benchmarks. Perfect for getting an overview of the library's capabilities and use cases for in-process communication.

### [Getting Started](./getting_started.md)
Step-by-step guide to integrate Signals into your Go application. Includes practical examples for monolithic e-commerce applications and package coordination patterns.

### [Core Concepts](./concepts.md)
Master the fundamental concepts and design patterns. Learn when to use async vs sync signals, understand the architecture scope, and explore advanced patterns for in-process event-driven systems.

### [API Reference](./api_reference.md)
Complete API documentation with all methods, interfaces, and usage examples. Covers both AsyncSignal and SyncSignal with detailed code samples.

### [Architecture](./architecture.md)
Deep dive into the engineering excellence behind the 5.66ns/op performance. Understand the zero-allocation design, memory management, and concurrency patterns.

---

## Documentation Overview

### For Beginners
1. **Start with [Introduction](./introduction.md)** - Understand what Signals is and its performance characteristics
2. **Follow [Getting Started](./getting_started.md)** - Implement your first signals in a real application
3. **Review [Core Concepts](./concepts.md)** - Learn best practices and design patterns

### For Advanced Users
1. **Study [Architecture](./architecture.md)** - Understand the internal implementation and optimizations
2. **Reference [API Documentation](./api_reference.md)** - Complete method reference and advanced usage

### For Integration
- **Quick Setup**: See [Getting Started - Installation](./getting_started.md#installation)
- **Choose Signal Type**: Review [Concepts - Signal Types](./concepts.md#signal-types)
- **Error Handling**: Check [API Reference - SyncSignal](./api_reference.md#syncsignal)
- **Performance Tuning**: Read [Architecture - Optimizations](./architecture.md#performance-optimizations)

## Key Features Covered

### Performance
- **5.66ns/op** single listener emit with zero allocations
- **Enterprise-grade** concurrency testing and validation
- **Zero-allocation** critical paths for high-frequency operations

### Reliability
- **Context-aware** error propagation with `TryEmit`
- **Transaction-safe** patterns for database operations
- **Race-condition free** design tested under extreme concurrency

### Ease of Use
- **Type-safe** generic APIs for compile-time safety
- **Fire-and-forget** async operations with AsyncSignal
- **Error-safe** sync operations with SyncSignal

## Common Use Cases

### In-Process Communication (Perfect Fit)
- **Package coordination** within monolithic applications
- **Component decoupling** in single Go binaries
- **Event-driven architecture** within one process
- **Plugin systems** and modular monoliths

### When NOT to Use
- **Microservices communication** (use message brokers)
- **Cross-process events** (use IPC mechanisms)
- **Distributed systems** (use event streaming platforms)

## Quick Examples

### Simple Async Events
```go
var UserRegistered = signals.New[User]()

UserRegistered.AddListener(func(ctx context.Context, user User) {
    sendWelcomeEmail(user)
})

UserRegistered.Emit(ctx, user) // Fire-and-forget
```

### Transaction-Safe Operations
```go
var OrderProcessed = signals.NewSync[Order]()

OrderProcessed.AddListenerWithErr(func(ctx context.Context, order Order) error {
    return processPayment(order) // Can return errors
})

if err := OrderProcessed.TryEmit(ctx, order); err != nil {
    // Handle error - subsequent listeners won't execute
}
```

## Performance Benchmarks

| Benchmark | Iterations | Time/Op | Memory/Op | Allocs/Op |
|-----------|------------|---------|-----------|-----------|
| **Single Listener** | 196,613,109 | **5.66ns** | **0 B** | **0 allocs** |
| **Concurrent Emit** | 41,751,328 | **28.55ns** | **0 B** | **0 allocs** |
| **100 Listeners** | 34,066 | 35.87μs | 42 B | 2 allocs |

## Contributing to Documentation

Found an issue or want to improve the docs?

1. **File Issues**: Report documentation bugs or unclear sections
2. **Suggest Improvements**: Propose new examples or use cases
3. **Add Examples**: Contribute real-world usage patterns

---

## External Links

- **GitHub Repository**: [https://github.com/maniartech/signals](https://github.com/maniartech/signals)
- **Go Package Documentation**: [https://godoc.org/github.com/maniartech/signals](https://godoc.org/github.com/maniartech/signals)
- **Performance Benchmarks**: Run `go test -bench=.` in the repository
- **Example Applications**: See `/example` directory in the repository

**Ready to get started?** Begin with the [Introduction](./introduction.md) →