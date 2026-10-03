package booking

import "errors"

var (
	ErrSeatAlreadyBooked = errors.New("Seat is already taken")
)

// Booking represents a confirmed seat reservation.
type Booking struct {
	ID      string
	MovieID string
	SeatID  string
	UserID  string
	Status  string
}

type BookingStore interface {
	Book(b Booking) error
	ListBookings(MovieID string) []Booking
}
