package booking

import (
	"context"
	"encoding/json"
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

func (s *RedisStore) Book(b Booking) (Booking, error) {
	session, err := s.hold(b)
	if err != nil {
		return Booking{}, err
	}

	log.Printf("Session booked %v", session)

	return session, nil
}

func (s *RedisStore) ListBookings(movieID string) []Booking {
	pattern := fmt.Sprintf("seat:%s:*", movieID)
	var sessions []Booking

	ctx := context.Background()

	iter := s.rdb.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		val, err := s.rdb.Get(ctx, iter.Val()).Result()
		if err != nil {
			continue
		}
		session, err := parseSession(val)
		if err != nil {
			continue
		}

		sessions = append(sessions, session)
	}

	return sessions
}

func (s *RedisStore) hold(b Booking) (Booking, error) {
	id := uuid.New().String()
	now := time.Now()
	ctx := context.Background()
	seatKey := fmt.Sprintf("seat:%s:%s", b.MovieID, b.SeatID)

	session := Booking{
		ID:        id,
		MovieID:   b.MovieID,
		SeatID:    b.SeatID,
		UserID:    b.UserID,
		Status:    "held",
		ExpiresAt: now.Add(defaultHoldTTL),
	}

	val, _ := json.Marshal(session)

	res := s.rdb.SetArgs(ctx, seatKey, val, redis.SetArgs{
		Mode: "NX", // set if not exists
		TTL:  defaultHoldTTL,
	})
	if res.Val() != "OK" {
		return Booking{}, ErrSeatAlreadyBooked
	}

	// Store reverse-lookup: session:{id} -> seat key
	// TTL matches the seat hold so both expire together
	if err := s.rdb.Set(ctx, sessionKey(id), seatKey, defaultHoldTTL).Err(); err != nil {
		// Seat key was set; roll it back to avoid a ghost hold
		s.rdb.Del(ctx, seatKey)
		return Booking{}, err
	}

	return session, nil
}

func parseSession(val string) (Booking, error) {
	var data Booking
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return Booking{}, err
	}
	return Booking{
		ID:      data.ID,
		MovieID: data.MovieID,
		SeatID:  data.SeatID,
		UserID:  data.UserID,
		Status:  data.Status,
	}, nil
}

/*
Confirm converts a held session into a permanent booking.
Removes the TTL (PERSIST) so the key never expires.
*/
func (s *RedisStore) Confirm(ctx context.Context, sessionID string, userID string) (Booking, error) {
	session, sk, err := s.getSession(ctx, sessionID, userID)
	if err != nil {
		return Booking{}, err
	}

	s.rdb.Persist(ctx, sk)
	s.rdb.Persist(ctx, sessionKey(sessionID))

	session.Status = "confirmed"
	data := Booking{
		ID:      session.ID,
		MovieID: string(session.MovieID),
		SeatID:  session.SeatID,
		UserID:  session.UserID,
		Status:  "confirmed",
	}
	val, _ := json.Marshal(data)
	s.rdb.Set(ctx, sk, val, 0)

	return session, nil
}

func (s *RedisStore) getSession(ctx context.Context, sessionID string, userID string) (Booking, string, error) {
	sk, err := s.rdb.Get(ctx, sessionKey(sessionID)).Result()
	if err != nil {
		return Booking{}, "", err
	}

	val, err := s.rdb.Get(ctx, sk).Result()
	if err != nil {
		return Booking{}, "", err
	}

	session, err := parseSession(val)
	if err != nil {
		return Booking{}, "", err
	}

	return session, sk, nil
}

func (s *RedisStore) Release(ctx context.Context, sessionID string, userID string) error {
	_, sk, err := s.getSession(ctx, sessionID, userID)
	if err != nil {
		return err
	}

	s.rdb.Del(ctx, sk, sessionKey(sessionID))
	return nil
}
