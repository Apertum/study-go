package main

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type zlibWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (w zlibWriter) Write(b []byte) (int, error) {
	// w.Writer будет отвечать за zlib-сжатие, поэтому пишем в него
	return w.Writer.Write(b)
}

func defaultHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	io.WriteString(w, "<html><body>"+strings.Repeat("Hello, world<br>", 20)+"</body></html>")
}

func deflateHandle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// проверяем, что клиент поддерживает deflate-сжатие
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "deflate") {
			next.ServeHTTP(w, r)
			return
		}
		flatew, err := zlib.NewWriterLevel(w, flate.BestCompression)
		if err != nil {
			io.WriteString(w, err.Error())
			return
		}
		defer flatew.Close()

		w.Header().Set("Content-Encoding", "deflate")
		next.ServeHTTP(zlibWriter{ResponseWriter: w, Writer: flatew}, r)
	})
}

/*
Аналог обработчика gzipHandle в разобранном выше примере,
который использует zlib формат вместо gzip.
В этом случае нужно проверять Accept-Encoding на наличие deflate.
*/
func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", defaultHandle)
	http.ListenAndServe(":3000", deflateHandle(mux))
}

// LengthHandle возвращает размер распакованных данных.
func LengthHandle(w http.ResponseWriter, r *http.Request) {
	// переменная reader будет равна r.Body или *gzip.Reader
	var reader io.Reader

	if r.Header.Get(`Content-Encoding`) == `gzip` { // Этот метод возвращает только первое значение.
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reader = gz
		defer gz.Close()
	} else {
		reader = r.Body
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "Length: %d", len(body))
}
