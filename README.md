# Concurrent Cinema Booking

A Go-based cinema seat-booking system built to demonstrate **double
booking, concurrency control, and atomic seat reservation**.

## Problem: Double Booking

The central problem is preventing two users from successfully booking
the same seat at the same time.

A naive flow looks like:

``` text
User A -> check A1 -> available
User B -> check A1 -> available
User A -> book A1
User B -> book A1
```

The check and the booking must therefore be treated as one atomic
operation.

The project demonstrates three progressively stronger solutions:

1.  **Memory Store** --- simple in-memory baseline.
2.  **Concurrency Solution** --- mutex-protected in-memory store.
3.  **Redis Solution** --- shared Redis storage with atomic `SET NX`,
    temporary holds, confirmation, and release.

------------------------------------------------------------------------

## 1. Memory Store

The first implementation stores bookings in a Go map:

``` go
type MemoryStore struct {
    bookings map[string]Booking
}
```

The basic operation checks whether the seat exists and then inserts it:

``` go
if _, exists := s.bookings[b.SeatID]; exists {
    return ErrSeatAlreadyBooked
}

s.bookings[b.SeatID] = b
```

This is useful as a simple baseline, but it is **not safe for concurrent
booking requests**. The map and the check-then-insert sequence are not
protected by synchronization.

The important lesson is that checking whether a seat is free and
reserving it cannot be treated as two independent operations when
requests can run concurrently.

------------------------------------------------------------------------

## 2. Concurrency Solution

The second implementation keeps the bookings in memory but protects them
with a Go `sync.RWMutex`:

``` go
type ConcurrentStore struct {
    bookings map[string]Booking
    sync.RWMutex
}
```

Booking acquires a write lock:

``` go
func (s *ConcurrentStore) Book(b Booking) error {
    s.Lock()
    defer s.Unlock()

    if _, exists := s.bookings[b.SeatID]; exists {
        return ErrSeatAlreadyBooked
    }

    s.bookings[b.SeatID] = b
    return nil
}
```

This makes the check-and-insert operation exclusive:

``` text
User A -> LOCK -> check -> book -> UNLOCK
User B -> LOCK -> check -> already booked -> UNLOCK
```

`ListBookings` uses `RLock`, allowing safe concurrent reads.

### Limitation

The mutex protects only the memory inside one Go process. If the
application runs as multiple instances, each instance has its own map
and its own mutex. The instances cannot coordinate bookings with each
other.

------------------------------------------------------------------------

## 3. Redis Solution

The third implementation uses Redis as shared storage.

The Redis key design is:

``` text
seat:{movieID}:{seatID} -> booking/session JSON
session:{sessionID}    -> seat key
```

The critical operation is Redis `SET` with `NX`:

``` go
s.rdb.SetArgs(ctx, key, val, redis.SetArgs{
    Mode: "NX",
    TTL:  defaultHoldTTL,
})
```

`NX` means the key is created only if it does not already exist.

Therefore, concurrent requests for the same seat are resolved atomically
by Redis:

``` text
1000 concurrent requests
          |
          v
      Redis SET NX
          |
     +----+----+
     |         |
   one OK    rest fail
```

This provides the concurrency guarantee required by the booking system.

### Temporary Seat Holds

A successful reservation initially has status:

``` text
held
```

and a default TTL of **2 minutes**.

If the user does not confirm the booking, Redis automatically expires
the seat key and the seat becomes available again.

### Confirmation

When a user confirms a held session, the Redis TTL is removed using
`PERSIST` and the booking status becomes:

``` text
confirmed
```

The seat therefore remains booked permanently.

### Release

A user can release a held session. The seat key and session key are
deleted, making the seat available again.

------------------------------------------------------------------------

# Architecture

``` text
Browser
   |
   | HTTP
   v
Handler
   |
   v
Service
   |
   v
BookingStore
   |
   +----------------+-------------------+
   |                |                   |
   v                v                   v
MemoryStore   ConcurrentStore      RedisStore
                                     |
                                     v
                                   Redis
```

The common abstraction is:

``` go
type BookingStore interface {
    Book(b Booking) (Booking, error)
    ListBookings(MovieID string) []Booking
    Confirm(ctx context.Context, sessionID string, userID string) (Booking, error)
    Release(ctx context.Context, sessionID string, userID string) error
}
```

This allows the service layer to use different storage implementations
without changing the HTTP API.

------------------------------------------------------------------------

# Web API

The server runs on:

``` text
http://localhost:8080
```

## `GET /movies`

Returns the available movies and their seat layout.

Example:

``` json
[
  {
    "id": "Avengers Doomsday",
    "title": "Avengers Doomsday",
    "rows": 5,
    "seats_per_row": 8
  }
]
```

## `GET /movies/{movieID}/seats`

Returns the current booking status of seats for a movie.

Example:

``` json
[
  {
    "seat_id": "A4",
    "user_id": "f96018f7c178",
    "booked": true,
    "confirmed": false
  }
]
```

The frontend uses these states to display:

-   Available --- gray
-   Your hold --- yellow
-   Other user's hold --- orange
-   Confirmed --- red

## `POST /movies/{movieID}/seats/{seatID}/hold`

Temporarily holds a seat.

Request:

``` json
{
  "user_id": "f96018f7c178"
}
```

Response contains the session ID, seat ID, movie ID, and expiration
time:

``` json
{
  "session_id": "4ba10978-...",
  "movieID": "Avengers Doomsday",
  "seat_id": "A4",
  "expires_at": "..."
}
```

## `PUT /sessions/{sessionID}/confirm`

Confirms a held seat.

Request:

``` json
{
  "user_id": "f96018f7c178"
}
```

The temporary hold becomes a confirmed booking.

## `DELETE /sessions/{sessionID}`

Releases a held seat.

Request:

``` json
{
  "user_id": "f96018f7c178"
}
```

The seat becomes available again.

------------------------------------------------------------------------

# Frontend

The project includes a browser UI for the complete booking flow:

-   Movie selection
-   Seat layout
-   Seat availability
-   Two-minute hold timer
-   Confirm booking
-   Release booking
-   Visual seat states
-   Polling for changes from other users

The browser polls the seat endpoint every two seconds so that separate
browser sessions can observe booking changes.

Example:

``` text
Browser A                    Browser B

Hold A4
   |
   v
 Redis
   |
   +----------------------+
                          |
                          v
                    A4 is unavailable
                          |
                          v
                   Browser B shows
                    "Other hold"
```

------------------------------------------------------------------------

# Concurrency Test

The project includes a concurrency test that launches **1,000
goroutines** attempting to book the same seat.

The expected result is:

``` text
Successful bookings: 1
Failed bookings:     999
```

This directly tests the main requirement of the project:

> Exactly one concurrent request should successfully reserve a
> particular seat.

------------------------------------------------------------------------

# Running the Project

## Start Redis

``` bash
docker compose up -d
```

Redis runs on:

``` text
localhost:6379
```

Redis Commander is available at:

``` text
http://localhost:8081
```

## Start the Go server

``` bash
go run .
```

Then open:

``` text
http://localhost:8080
```

------------------------------------------------------------------------

# Project Structure

``` text
.
├── main.go
├── index.html
├── docker-compose.yaml
├── domain.go
├── service.go
├── handler.go
├── memory_store.go
├── concurrent_store.go
├── redis_store.go
├── redis.go
├── utils.go
└── service_test.go
```

  File                    Responsibility
  ----------------------- -------------------------------------
  `main.go`               HTTP server, routes, and movie data
  `index.html`            Cinema booking frontend
  `domain.go`             Booking model and store interface
  `service.go`            Booking service layer
  `handler.go`            HTTP API handlers
  `memory_store.go`       Basic in-memory implementation
  `concurrent_store.go`   Mutex-protected implementation
  `redis_store.go`        Redis implementation
  `redis.go`              Redis client initialization
  `service_test.go`       Concurrent booking test
  `docker-compose.yaml`   Redis and Redis Commander

------------------------------------------------------------------------

# Solution Comparison

  --------------------------------------------------------------------------
  Solution       Storage        Concurrency    Multiple       Temporary Hold
                                               Instances      
  -------------- -------------- -------------- -------------- --------------
  Memory Store   Go map         No             No             No

  Concurrent     Go map         Yes --- mutex  No             No
  Store                                                       

  Redis Store    Redis          Yes --- atomic Yes            Yes --- TTL
                                `SET NX`                      
  --------------------------------------------------------------------------

The progression is:

``` text
Simple in-memory storage
          ↓
Mutex-based concurrency control
          ↓
Shared Redis + atomic reservation
```

## Key Takeaway

The main challenge in cinema booking is not storing a booking. It is
making the **seat reservation operation atomic under concurrent
requests**.

This project demonstrates how the solution evolves from a simple
in-memory implementation, to process-level synchronization with a mutex,
and finally to a Redis-based distributed solution using atomic `SET NX`.

The Redis implementation also adds the concepts required for a real
booking flow: **temporary holds, expiration, confirmation, and
release**.
