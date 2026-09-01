package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// ============================================================
// MODEL
// ============================================================

type Item struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
}

// ============================================================
// REQUEST DTO
//
// Client'ın oluşturma/güncelleme sırasında göndereceği veri.
// ID client tarafından gönderilmiyor.
// ID server tarafından oluşturuluyor.
// ============================================================

type ItemRequest struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
}

// ============================================================
// STORE ERRORS
// ============================================================

var (
	ErrItemNotFound = errors.New("item not found")
	ErrInvalidItem  = errors.New("invalid item")
)

// ============================================================
// STORE LAYER
//
// Handler'ların veri yapısını doğrudan yönetmesini engelliyoruz.
//
// Gerçek projede bu layer ileride:
//
// map
//   ↓
// PostgreSQL
//
// şeklinde değiştirilebilir.
// ============================================================

type ItemStore struct {
	mu     sync.RWMutex
	items  map[int]Item
	nextID int
}

// ============================================================
// STORE CONSTRUCTOR
// ============================================================

func NewItemStore() *ItemStore {
	return &ItemStore{
		items: map[int]Item{
			1: {
				ID:          1,
				Name:        "MacBook Pro",
				Description: "Development laptop",
				Price:       89999,
			},
			2: {
				ID:          2,
				Name:        "Mechanical Keyboard",
				Description: "Go development keyboard",
				Price:       4999,
			},
		},
		nextID: 3,
	}
}

// ============================================================
// CREATE
//
// POST /items
// ============================================================

func (s *ItemStore) Create(req ItemRequest) (Item, error) {
	if strings.TrimSpace(req.Name) == "" {
		return Item{}, ErrInvalidItem
	}

	if req.Price < 0 {
		return Item{}, ErrInvalidItem
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	item := Item{
		ID:          s.nextID,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Price:       req.Price,
	}

	s.items[item.ID] = item
	s.nextID++

	return item, nil
}

// ============================================================
// READ ALL
//
// GET /items
// ============================================================

func (s *ItemStore) GetAll() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Item, 0, len(s.items))

	for _, item := range s.items {
		items = append(items, item)
	}

	return items
}

// ============================================================
// READ ONE
//
// GET /items/{id}
// ============================================================

func (s *ItemStore) GetByID(id int) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	item, exists := s.items[id]

	if !exists {
		return Item{}, ErrItemNotFound
	}

	return item, nil
}

// ============================================================
// UPDATE
//
// PUT /items/{id}
// ============================================================

func (s *ItemStore) Update(id int, req ItemRequest) (Item, error) {
	if strings.TrimSpace(req.Name) == "" {
		return Item{}, ErrInvalidItem
	}

	if req.Price < 0 {
		return Item{}, ErrInvalidItem
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.items[id]; !exists {
		return Item{}, ErrItemNotFound
	}

	updatedItem := Item{
		ID:          id,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Price:       req.Price,
	}

	s.items[id] = updatedItem

	return updatedItem, nil
}

// ============================================================
// DELETE
//
// DELETE /items/{id}
// ============================================================

func (s *ItemStore) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.items[id]; !exists {
		return ErrItemNotFound
	}

	delete(s.items, id)

	return nil
}

// ============================================================
// HTTP SERVER
// ============================================================

type Server struct {
	store *ItemStore
}

// ============================================================
// CREATE HANDLER
//
// POST /items
//
// Task:
// ✓ Decode JSON body
// ✓ Create new record
// ✓ Return 201 Created
// ============================================================

func (s *Server) createItemHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req ItemRequest

	decoder := json.NewDecoder(r.Body)

	if err := decoder.Decode(&req); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid JSON body",
		)

		return
	}

	item, err := s.store.Create(req)

	if err != nil {
		if errors.Is(err, ErrInvalidItem) {
			writeJSONError(
				w,
				http.StatusBadRequest,
				"invalid item",
			)

			return
		}

		writeJSONError(
			w,
			http.StatusInternalServerError,
			"failed to create item",
		)

		return
	}

	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.Header().Set(
		"Location",
		fmt.Sprintf("/items/%d", item.ID),
	)

	w.WriteHeader(http.StatusCreated)

	if err := json.NewEncoder(w).Encode(item); err != nil {
		log.Printf(
			"failed to encode created item: %v",
			err,
		)
	}
}

// ============================================================
// READ ALL HANDLER
//
// GET /items
//
// Task:
// ✓ Return all records
// ✓ Return 200 OK
// ============================================================

func (s *Server) listItemsHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	items := s.store.GetAll()

	writeJSON(
		w,
		http.StatusOK,
		items,
	)
}

// ============================================================
// READ ONE HANDLER
//
// GET /items/{id}
//
// Task:
// ✓ Parse ID
// ✓ Find record
// ✓ Return 404 when missing
// ✓ Return 200 when found
// ============================================================

func (s *Server) getItemHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseID(r)

	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid item id",
		)

		return
	}

	item, err := s.store.GetByID(id)

	if err != nil {
		if errors.Is(err, ErrItemNotFound) {
			writeJSONError(
				w,
				http.StatusNotFound,
				"item not found",
			)

			return
		}

		writeJSONError(
			w,
			http.StatusInternalServerError,
			"failed to get item",
		)

		return
	}

	writeJSON(
		w,
		http.StatusOK,
		item,
	)
}

// ============================================================
// UPDATE HANDLER
//
// PUT /items/{id}
//
// Task:
// ✓ Parse ID
// ✓ Decode JSON
// ✓ Update existing record
// ✓ Return 404 if missing
// ✓ Return 200 OK
// ============================================================

func (s *Server) updateItemHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseID(r)

	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid item id",
		)

		return
	}

	var req ItemRequest

	decoder := json.NewDecoder(r.Body)

	if err := decoder.Decode(&req); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid JSON body",
		)

		return
	}

	item, err := s.store.Update(id, req)

	if err != nil {
		switch {
		case errors.Is(err, ErrItemNotFound):
			writeJSONError(
				w,
				http.StatusNotFound,
				"item not found",
			)

		case errors.Is(err, ErrInvalidItem):
			writeJSONError(
				w,
				http.StatusBadRequest,
				"invalid item",
			)

		default:
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"failed to update item",
			)
		}

		return
	}

	writeJSON(
		w,
		http.StatusOK,
		item,
	)
}

// ============================================================
// DELETE HANDLER
//
// DELETE /items/{id}
//
// Task:
// ✓ Parse ID
// ✓ Delete record
// ✓ Return 404 if missing
// ✓ Return 204 No Content on success
// ============================================================

func (s *Server) deleteItemHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseID(r)

	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid item id",
		)

		return
	}

	err = s.store.Delete(id)

	if err != nil {
		if errors.Is(err, ErrItemNotFound) {
			writeJSONError(
				w,
				http.StatusNotFound,
				"item not found",
			)

			return
		}

		writeJSONError(
			w,
			http.StatusInternalServerError,
			"failed to delete item",
		)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// ID PARSER
//
// /items/42
//
// PathValue("id")
//       ↓
//      "42"
//       ↓
// strconv.Atoi()
//       ↓
//       42
// ============================================================

func parseID(r *http.Request) (int, error) {
	idString := r.PathValue("id")

	id, err := strconv.Atoi(idString)

	if err != nil {
		return 0, err
	}

	if id <= 0 {
		return 0, fmt.Errorf(
			"invalid id: %d",
			id,
		)
	}

	return id, nil
}

// ============================================================
// JSON RESPONSE HELPERS
// ============================================================

func writeJSON(
	w http.ResponseWriter,
	status int,
	data any,
) {
	w.Header().Set(
		"Content-Type",
		"application/json",
	)

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf(
			"failed to encode JSON response: %v",
			err,
		)
	}
}

// ============================================================
// JSON ERROR RESPONSE
// ============================================================

func writeJSONError(
	w http.ResponseWriter,
	status int,
	message string,
) {
	response := map[string]string{
		"error": message,
	}

	writeJSON(
		w,
		status,
		response,
	)
}

// ============================================================
// ROUTER
//
// REST API:
//
// POST   /items
// GET    /items
// GET    /items/{id}
// PUT    /items/{id}
// DELETE /items/{id}
// ============================================================

func (s *Server) router() http.Handler {
	mux := http.NewServeMux()

	// --------------------------------------------------------
	// CREATE
	// --------------------------------------------------------

	mux.HandleFunc(
		"POST /items",
		s.createItemHandler,
	)

	// --------------------------------------------------------
	// READ ALL
	// --------------------------------------------------------

	mux.HandleFunc(
		"GET /items",
		s.listItemsHandler,
	)

	// --------------------------------------------------------
	// READ ONE
	// --------------------------------------------------------

	mux.HandleFunc(
		"GET /items/{id}",
		s.getItemHandler,
	)

	// --------------------------------------------------------
	// UPDATE
	// --------------------------------------------------------

	mux.HandleFunc(
		"PUT /items/{id}",
		s.updateItemHandler,
	)

	// --------------------------------------------------------
	// DELETE
	// --------------------------------------------------------

	mux.HandleFunc(
		"DELETE /items/{id}",
		s.deleteItemHandler,
	)

	return loggingMiddleware(mux)
}

// ============================================================
// LOGGING MIDDLEWARE
// ============================================================

func loggingMiddleware(
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(
		func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			log.Printf(
				"%s %s",
				r.Method,
				r.URL.Path,
			)

			next.ServeHTTP(w, r)
		},
	)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	store := NewItemStore()

	server := &Server{
		store: store,
	}

	router := server.router()

	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	log.Println(
		"REST API listening on http://localhost:8080",
	)

	log.Println(
		"Available endpoints:",
	)

	log.Println(
		"POST   /items",
	)

	log.Println(
		"GET    /items",
	)

	log.Println(
		"GET    /items/{id}",
	)

	log.Println(
		"PUT    /items/{id}",
	)

	log.Println(
		"DELETE /items/{id}",
	)

	if err := httpServer.ListenAndServe(); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Fatalf(
			"server failed: %v",
			err,
		)
	}
}

// ============================================================
// EXAMPLE REQUESTS
//
// CREATE
//
// curl -X POST http://localhost:8080/items \
//   -H "Content-Type: application/json" \
//   -d '{
//     "name": "Monitor",
//     "description": "4K developer monitor",
//     "price": 25000
//   }'
//
// Response:
//
// 201 Created
//
// {
//   "id": 3,
//   "name": "Monitor",
//   "description": "4K developer monitor",
//   "price": 25000
// }
//
// ------------------------------------------------------------
//
// READ ALL
//
// curl http://localhost:8080/items
//
// Response:
//
// 200 OK
//
// [
//   {
//     "id": 1,
//     "name": "MacBook Pro",
//     "description": "Development laptop",
//     "price": 89999
//   },
//   ...
// ]
//
// ------------------------------------------------------------
//
// READ ONE
//
// curl http://localhost:8080/items/1
//
// Response:
//
// 200 OK
//
// ------------------------------------------------------------
//
// READ MISSING ITEM
//
// curl http://localhost:8080/items/999
//
// Response:
//
// 404 Not Found
//
// {
//   "error": "item not found"
// }
//
// ------------------------------------------------------------
//
// UPDATE
//
// curl -X PUT http://localhost:8080/items/1 \
//   -H "Content-Type: application/json" \
//   -d '{
//     "name": "MacBook Pro M5",
//     "description": "Updated development laptop",
//     "price": 99999
//   }'
//
// Response:
//
// 200 OK
//
// ------------------------------------------------------------
//
// DELETE
//
// curl -X DELETE http://localhost:8080/items/1
//
// Response:
//
// 204 No Content
//
// ------------------------------------------------------------
//
// DELETE AGAIN
//
// curl -X DELETE http://localhost:8080/items/1
//
// Response:
//
// 404 Not Found
//
// ============================================================
//
// DAY 32 TASK CHECKLIST
//
// ✓ Implement Create
//   POST /items
//   JSON decoding
//   Store.Create()
//   201 Created
//
// ✓ Implement Read
//   GET /items
//   GET /items/{id}
//   PathValue()
//   404 handling
//
// ✓ Implement Update
//   PUT /items/{id}
//   JSON decoding
//   Store.Update()
//   404 handling
//
// ✓ Implement Delete
//   DELETE /items/{id}
//   Store.Delete()
//   204 No Content
//   404 handling
//
// ✓ Handler layer
// ✓ Store layer
// ✓ Explicit HTTP status codes
// ✓ JSON responses
// ✓ REST-style resource endpoints
// ✓ ID path parameters
// ✓ Concurrent-safe in-memory store
//
// ============================================================
