# Introduction



**signals** is a robust, dependency-free Go library that provides a simple, thin, and user-friendly in-process signaling mechanism for your Go applications. It enables you to generate and emit signals (synchronously or asynchronously) and manage listeners with ease, facilitating decoupled communication between components within the same process.

## Objectives
- Decoupled, event-driven architecture for Go applications
- 100% thread-safe and production-ready
- Minimal allocations and high throughput
- Simple, type-safe API for both synchronous and asynchronous event delivery
- Easy listener management (add, remove, reset) with optional keys
- Designed for real-time, concurrent, and testable systems

- Synchronous and asynchronous signal dispatching
- Type-safe, generic signal and listener definitions
- Listener management with optional keys for removal
- Full test coverage and proven in production
- No external dependencies

## Applications & Use Cases

**signals** is ideal for:

- Decoupling components in large Go applications
- Implementing event-driven architectures
- Building plugin or extension systems
- Real-time data processing pipelines
- GUI or CLI event handling
- In-process pub-sub for microservices or modular monoliths
- Observing state changes or propagating updates
- Testing and mocking event flows
- Orchestrating background jobs and workflows

The library is suitable for any scenario where you need safe, efficient, and flexible in-process event or signal delivery between independent parts of your Go application.
- Synchronous and asynchronous signal dispatching
- Type-safe, generic signal and listener definitions
- Listener management with optional keys for removal
- Full test coverage and proven in production
- No external dependencies


- [Getting Started](getting_started.md)
- [Concepts](concepts.md)
- [API Reference](api_reference.md)
