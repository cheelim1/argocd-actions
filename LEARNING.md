# Learnings !

## Semaphore:
A semaphore is a synchronization primitive that is used to control access to a shared resource. It's often used to limit the number of threads or goroutines that can access a specific resource or perform a given action at the same time.

Use case:
- Used to limit the number of concurrent sync operations. This is done to prevent overwhelming the server, which might be one of the reasons you're seeing transient errors.

## Exponential Backoff with Jitter:
Exponential backoff is a standard error-handling strategy for network applications in which the client increases the wait time between retries exponentially, up to a maximum number of retries. The idea is to give the system you're communicating with more time to recover from errors as they persist.
However, if many clients start their exponential backoff at the same time, they might end up retrying at the same moments, leading to "retry storms." This is where jitter comes in.

Jitter introduces a random variation to the exponential backoff delay. This ensures that not all clients will retry at the same time, spreading out the retries and reducing the load on the server.
By combining exponential backoff with jitter, you're making your retry mechanism more efficient and less likely to overwhelm the server, especially in scenarios where many clients might be retrying simultaneously.