package products

import (
	repo "go-project/internal/adapters/postgresql/sqlc"
	"go-project/internal/utils"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type handler struct {
	service Service
}

func NewHandler(service Service) *handler {
	return &handler{
		service: service,
	}
}

// [GET] List products
func (h *handler) ListProducts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := query.Get("name")
	priceStr := query.Get("price_in_centers")

	if name != "" || priceStr != "" {
		h.FindProductsByQuery(w, r)
		return
	}

	products, err := h.service.ListProducts(r.Context())
	if err != nil {
		log.Println(err)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	utils.Write(w, http.StatusOK, products)
}

func (h *handler) FindProductsByQuery(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := query.Get("name")
	priceStr := query.Get("price_in_centers")

	var priceInCenters int32 = 0

	if priceStr != "" {
		val, err := strconv.ParseInt(priceStr, 10, 32)
		if err != nil {
			http.Error(w, "Invalid price format", http.StatusBadRequest)
			return
		}
		priceInCenters = int32(val)
	}

	filter := repo.FindProductsByQueryParams{
		Name:           name,
		PriceInCenters: priceInCenters,
	}

	products, err := h.service.FindProductsByQuery(r.Context(), filter)
	if err != nil {
		log.Println(err)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	utils.Write(w, http.StatusOK, products)
}

func (h *handler) FindProductByID(w http.ResponseWriter, r *http.Request) {
	data := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(data, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}
	product, err := h.service.FindProductByID(r.Context(), id)
	if err != nil {
		log.Println(err)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	utils.Write(w, http.StatusOK, product)
}
