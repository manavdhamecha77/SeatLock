package main

import (
	"log"
	"net/http"

	"github.com/manavdhamecha77/Concurrent-Cinema-Booking/internal/booking"
	"github.com/manavdhamecha77/Concurrent-Cinema-Booking/internal/utils"
	"github.com/redis/go-redis/v9"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /movies", listMovies)

	mux.Handle("GET /", http.FileServer(http.Dir("static")))

	store := booking.NewRedisStore(redis.NewClient("localhost:6379"))

	svc := booking.NewService(store)

	bookingHandler := booking.NewHandler(svc)

	mux.HandleFunc("GET /movies/:movieID/seats", bookingHandler.ListSeats)

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

type SeatInfo struct {
	SeatID string `json:seat_id"`
	UserID string `json:user_id"`
	Booked bool   `json:booked"`
}

var movies = []movieResponse{
	{ID: "Avengers Doomsday", Title: "Avengers Doomsday", Rows: 5, SeatsPerRow: 8},
	{ID: "Spierman and Deadpool", Title: "Spierman and Deadpool", Rows: 4, SeatsPerRow: 6},
}

func listMovies(w http.ResponseWriter, r *http.Request) {
	utils.WriteJSON(w, http.StatusOK, movies)
}

type movieResponse struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Rows        int    `json:"rows"`
	SeatsPerRow int    `json:"seats_per_row"`
}
