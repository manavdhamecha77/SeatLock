package booking

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const defaultHoldTTL = 2 * time.Minute

/*
Redis Store implements session-based seat booking by Redis.

Key design:

seat: {movieID}:{seatID} -> session JSON (TTL = held, no TTL = confirmed)
session:{sessionID}      -> seat key     (reserve lookup)
*/

type RedisStore struct {
	rdb *redis.Client
}

func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{
		rdb: rdb,
	}
}

// session key builds reverse-lookup key for a session.
func sessionKey(id string) string {
	return fmt.Sprintf("session:%s", id)
}

func (s *RedisStore) Book(b Booking) error {
	session, err := s.hold(b)
	if err != nil {
		return err
	}

	log.Printf("Session booked %v", session)

	return nil
}

func (s *RedisStore) ListBookings(movieID string) []Booking {
	return []Booking{}
}

func (s *RedisStore) hold(b Booking) (Booking, error) {
	id := uuid.New().String()
	now := time.Now()
	ctx := context.Background()
	key := fmt.Sprintf("seat:%s:%s", b.MovieID,b.SeatID)

	b.ID = id
	// val, _ := json.Marshal(b)

	res := s.rdb.SetArgs(ctx, key, b, redis.SetArgs{
		Mode: "NX", // set if not exists
		TTL: defaultHoldTTL,
	})
	ok := res.Val() == "OK"

	if !ok {
		return Booking{}, ErrSeatAlreadyBooked
	}

	return Booking{
		ID:      id,
		MovieID: b.MovieID,
		SeatID:  b.SeatID,
		UserID:  b.UserID,
		Status:  "held",
		ExpiresAt: now.Add(defaultHoldTTL),
	}, nil
}
