package booking

import "net/http"

type handler struct {
	svc Service
}

func NewHandler() *handler {
	return &handler{}
}

func (h *handler) ListSeats(w http.ResponseWriter, r *http.Request) {

}
