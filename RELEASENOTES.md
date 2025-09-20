# Release Notes

## Signals Library Release Notes - Version 1.2.0

We are excited to announce the release of Signals version 1.2.0. This version brings significant improvements and changes, enhancing the functionality and usability of our library.

### New Features and Enhancements

#### 1. Asynchronous Signal Emission
- **AsyncSignal.Emit**: This function now emits signals asynchronously to all listeners. Importantly, it waits for all listeners to finish before proceeding. This change ensures that all side effects of the signal are completed before the next line of code executes.

    **Example Usage:**
    ```go
    RecordCreated.Emit(ctx, Record{ID: 1, Name: "John"})
    fmt.PrintLine("Record Created")
    ```
    This code emits a signal indicating that a record has been created and waits for all listeners to process this event before printing "Record Created".

- **Non-Blocking AsyncSignal.Emit**: For scenarios where you don't need to wait for listeners to finish, you can now emit signals in a non-blocking manner by creating a go routine.

    **Example Usage:**
    ```go
    go RecordCreated.Emit(ctx, Record{ID: 1, Name: "John"})
    fmt.PrintLine("Record creation work in progress")
    ```
    This approach is useful for fire-and-forget scenarios where the completion of listeners is not critical to the flow of the program.

#### 2. Codebase Improvements
- **Code Cleanup**: We've gone through our codebase, refining and optimizing various parts to enhance performance and maintainability.
- **Enhanced Comments**: To improve understandability and ease of use, we've updated and expanded the comments throughout the codebase. This should make it easier for developers to understand and use our library effectively.

### Acknowledgements
We want to thank our community of developers for their continued support and feedback, which plays a vital role in the ongoing improvement of the Signals library.

### Get Started
To start using Signals version 1.2.0, please visit our [GitHub repository](https://github.com/maniartech/signals).

Stay tuned for more updates and happy coding!