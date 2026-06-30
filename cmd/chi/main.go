package main

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var cars = map[string]string{
	"id1": "Renault Logan",
	"id2": "Renault Duster",
	"id3": "BMW X6",
	"id4": "BMW M5",
	"id5": "VW Passat",
	"id6": "VW Jetta",
	"id7": "Audi A4",
	"id8": "Audi Q7",
}

// carsListFunc — вспомогательная функция для вывода всех машин.
func carsListFunc() []string {
	var list []string
	for _, c := range cars {
		list = append(list, c)
	}
	return list
}

// carFunc — вспомогательная функция для вывода определённой машины.
func carFunc(id string) string {
	if c, ok := cars[id]; ok {
		return c
	}
	return "unknown identifier " + id
}

func carsHandle(rw http.ResponseWriter, r *http.Request) {
	carsList := carsListFunc()
	io.WriteString(rw, strings.Join(carsList, ", "))
}

func carHandle(rw http.ResponseWriter, r *http.Request) {
	carID := r.URL.Query().Get("id")
	if carID == "" {
		http.Error(rw, "carID param is missed", http.StatusBadRequest)
		return
	}
	rw.Write([]byte(carFunc(carID)))
}

func newCar(rw http.ResponseWriter, r *http.Request) {
	carID := r.URL.Query().Get("id")
	if carID == "" {
		http.Error(rw, "carID param is missed", http.StatusBadRequest)
		return
	}
	rw.Write([]byte(carFunc(carID)))
}

func getCar(rw http.ResponseWriter, r *http.Request) {
	carID := r.URL.Query().Get("id")
	if carID == "" {
		http.Error(rw, "carID param is missed", http.StatusBadRequest)
		return
	}
	rw.Write([]byte(carFunc(carID)))
}

func updateCar(rw http.ResponseWriter, r *http.Request) {
	carID := r.URL.Query().Get("id")
	if carID == "" {
		http.Error(rw, "carID param is missed", http.StatusBadRequest)
		return
	}
	rw.Write([]byte(carFunc(carID)))
}

func deleteCar(rw http.ResponseWriter, r *http.Request) {
	carID := r.URL.Query().Get("id")
	if carID == "" {
		http.Error(rw, "carID param is missed", http.StatusBadRequest)
		return
	}
	rw.Write([]byte(carFunc(carID)))
}

func main() {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// или
	// r.Use(middleware.RealIP, middleware.Logger, middleware.Recoverer)

	r.Post("/car", newCar)           // POST /car
	r.Get("/car/{id}", getCar)       // GET /car/1234
	r.Put("/car/{id}", updateCar)    // PUT /car/1234
	r.Delete("/car/{id}", deleteCar) // DELETE /car/1234

	// то же самое, используя Router
	r.Route("/car", func(r chi.Router) {
		r.Post("/", newCar) // POST /car
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", getCar)       // GET /car/1234
			r.Put("/", updateCar)    // PUT /car/1234
			r.Delete("/", deleteCar) // DELETE /car/1234
		})
	})

	log.Fatal(http.ListenAndServe(":8080", r))
}

func TimerTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// перед началом выполнения функции сохраняем текущее время
		start := time.Now()
		// вызываем следующий обработчик
		next.ServeHTTP(w, r)
		// после завершения замеряем время выполнения запроса
		duration := time.Since(start)
		log.Fatal(duration)
		// сохраняем или сразу обрабатываем полученный результат
		// ...
	})
}
