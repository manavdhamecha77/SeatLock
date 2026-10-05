package booking

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/manavdhamecha77/Concurrent-Cinema-Booking/internal/adapters/redis"
)

// Metrics holds latency and throughput measurements
type Metrics struct {
	TotalRequests    int64
	SuccessfulReqs   int64
	FailedReqs       int64
	TotalDurationMs  float64
	Latencies        []float64 // in milliseconds
	ThroughputRPS    float64
	P50LatencyMs     float64
	P95LatencyMs     float64
	P99LatencyMs     float64
	AvgLatencyMs     float64
	MinLatencyMs     float64
	MaxLatencyMs     float64
}

func (m *Metrics) String() string {
	return fmt.Sprintf(`
Metrics Summary:
  Total Requests:     %d
  Successful:         %d
  Failed:             %d
  Total Duration:     %.2f ms
  Throughput:         %.2f RPS
  
Latency (ms):
  Average:            %.2f
  Min:                %.2f
  Max:                %.2f
  P50:                %.2f
  P95:                %.2f
  P99:                %.2f
`, m.TotalRequests, m.SuccessfulReqs, m.FailedReqs, m.TotalDurationMs, m.ThroughputRPS,
		m.AvgLatencyMs, m.MinLatencyMs, m.MaxLatencyMs, m.P50LatencyMs, m.P95LatencyMs, m.P99LatencyMs)
}

// calculatePercentile returns the Nth percentile from sorted latencies
func calculatePercentile(latencies []float64, percentile float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	index := int(float64(len(latencies)) * percentile / 100)
	if index >= len(latencies) {
		index = len(latencies) - 1
	}
	return latencies[index]
}

// measureConcurrentBooking runs concurrent booking requests and measures latency/throughput
func measureConcurrentBooking(svc *Service, numGoroutines int, seatPrefix string) *Metrics {
	metrics := &Metrics{
		TotalRequests: int64(numGoroutines),
		Latencies:     make([]float64, 0, numGoroutines),
	}

	var (
		successes      atomic.Int64
		failures       atomic.Int64
		latenciesMutex sync.Mutex
		wg             sync.WaitGroup
	)

	wg.Add(numGoroutines)
	startTime := time.Now()

	for i := range numGoroutines {
		go func(userNum int) {
			defer wg.Done()
			reqStart := time.Now()

			_, err := svc.Book(Booking{
				MovieID: "screen-1",
				SeatID:  seatPrefix + fmt.Sprintf("%d", userNum),
				UserID:  uuid.New().String(),
			})

			latency := time.Since(reqStart).Seconds() * 1000 // convert to ms

			latenciesMutex.Lock()
			metrics.Latencies = append(metrics.Latencies, latency)
			latenciesMutex.Unlock()

			if err == nil {
				successes.Add(1)
			} else {
				failures.Add(1)
			}
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	metrics.SuccessfulReqs = successes.Load()
	metrics.FailedReqs = failures.Load()
	metrics.TotalDurationMs = totalDuration.Seconds() * 1000
	metrics.ThroughputRPS = float64(numGoroutines) / totalDuration.Seconds()

	// Calculate latency percentiles
	if len(metrics.Latencies) > 0 {
		sort.Float64s(metrics.Latencies)

		var sum float64
		metrics.MinLatencyMs = metrics.Latencies[0]
		metrics.MaxLatencyMs = metrics.Latencies[len(metrics.Latencies)-1]

		for _, lat := range metrics.Latencies {
			sum += lat
		}
		metrics.AvgLatencyMs = sum / float64(len(metrics.Latencies))
		metrics.P50LatencyMs = calculatePercentile(metrics.Latencies, 50)
		metrics.P95LatencyMs = calculatePercentile(metrics.Latencies, 95)
		metrics.P99LatencyMs = calculatePercentile(metrics.Latencies, 99)
	}

	return metrics
}

// TestDoubleBookingPrevention_RedisStore verifies zero double-booking with Redis
func TestDoubleBookingPrevention_RedisStore(t *testing.T) {
	store := NewRedisStore(redis.NewClient("127.0.0.1:6379"))
	svc := NewService(store)

	const numGoroutines = 10000
	const expectedSuccesses = 1

	var (
		successes atomic.Int64
		failures  atomic.Int64
		wg        sync.WaitGroup
	)

	wg.Add(numGoroutines)
	for range numGoroutines {
		go func() {
			defer wg.Done()
			_, err := svc.Book(Booking{
				MovieID: "screen-doubletest",
				SeatID:  "A1",
				UserID:  uuid.New().String(),
			})
			if err == nil {
				successes.Add(1)
			} else {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != expectedSuccesses {
		t.Errorf("expected exactly %d success, got %d (double-booking detected!)", expectedSuccesses, got)
	}

	if got := failures.Load(); got != int64(numGoroutines-expectedSuccesses) {
		t.Errorf("expected %d failures, got %d", numGoroutines-1, got)
	}

	t.Logf("✓ Double-booking prevention: 100%% success rate (1/%d won)", numGoroutines)
}

// TestRedisStoreThroughput measures RPS for Redis implementation
func TestRedisStoreThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping throughput test in short mode")
	}

	store := NewRedisStore(redis.NewClient("127.0.0.1:6379"))
	svc := NewService(store)

	concurrencyLevels := []int{100, 1000}
	results := make(map[int]float64)

	for _, level := range concurrencyLevels {
		metrics := measureConcurrentBooking(svc, level, fmt.Sprintf("A"))
		results[level] = metrics.ThroughputRPS

		t.Logf("Redis Store @ %d concurrent: %.0f RPS (P50: %.2f ms, P95: %.2f ms, P99: %.2f ms)",
			level, metrics.ThroughputRPS, metrics.P50LatencyMs, metrics.P95LatencyMs, metrics.P99LatencyMs)
	}
}

// TestConfirmAndReleaseLatency measures latency for confirm/release operations
func TestConfirmAndReleaseLatency(t *testing.T) {
	store := NewRedisStore(redis.NewClient("127.0.0.1:6379"))
	svc := NewService(store)
	ctx := context.Background()

	// Hold a seat first
	booking, err := svc.Book(Booking{
		MovieID: "screen-1",
		SeatID:  "B5",
		UserID:  uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("failed to book: %v", err)
	}

	// Measure confirm latency
	confirmStart := time.Now()
	_, err = svc.ConfirmSeat(ctx, booking.ID, booking.UserID)
	confirmLatency := time.Since(confirmStart).Milliseconds()

	if err != nil {
		t.Fatalf("failed to confirm: %v", err)
	}

	t.Logf("Confirm latency: %d ms", confirmLatency)

	// Measure another hold and release
	booking2, err := svc.Book(Booking{
		MovieID: "screen-1",
		SeatID:  "B6",
		UserID:  uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("failed to book: %v", err)
	}

	releaseStart := time.Now()
	err = svc.ReleaseSeat(ctx, booking2.ID, booking2.UserID)
	releaseLatency := time.Since(releaseStart).Milliseconds()

	if err != nil {
		t.Fatalf("failed to release: %v", err)
	}

	t.Logf("Release latency: %d ms", releaseLatency)

	if confirmLatency > 1000 || releaseLatency > 1000 {
		t.Logf("⚠ High latency detected (>1s), investigate Redis connection")
	} else {
		t.Logf("✓ Operations completed within acceptable latency (<1s)")
	}
}
