package services

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vendas/loja/database"
)

func TestBuscaPorISBNJuntaAsBases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "isbn.db")
	t.Setenv("DB_PATH", path)
	database.Init(path)
	t.Cleanup(func() { database.DB.Close() })

	chamadas := 0
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamadas++
		if !strings.Contains(r.URL.Query().Get("q"), "9780306406157") {
			w.Write([]byte(`{"totalItems":0}`))
			return
		}
		w.Write([]byte(`{"items":[{"volumeInfo":{"title":"Vidas Secas","authors":["Graciliano Ramos"],"publisher":"Record",
			"language":"pt","description":"<p>Fabiano e sua família.</p>","imageLinks":{"thumbnail":"http://books.google.com/x?id=1&edge=curl"}}}]}`))
	}))
	defer google.Close()
	ol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ISBN:9780306406157":{"title":"Vidas secas","cover":{"large":"https://covers.openlibrary.org/b/id/1-L.jpg"}}}`))
	}))
	defer ol.Close()
	t.Setenv("ISBN_GOOGLE_URL", google.URL)
	t.Setenv("ISBN_OPENLIBRARY_URL", ol.URL)

	meta, found, err := LookupISBN("9780306406157")
	if err != nil || !found {
		t.Fatalf("não encontrou: %v", err)
	}
	if meta.Title != "Vidas Secas" || meta.Authors != "Graciliano Ramos" || meta.Language != "Português" ||
		meta.Description != "Fabiano e sua família." || meta.CoverURL != "https://covers.openlibrary.org/b/id/1-L.jpg" {
		t.Fatalf("dados = %+v", meta)
	}
	// Segunda consulta vem do cache.
	LookupISBN("9780306406157")
	if chamadas != 1 {
		t.Fatalf("consultou o Google %d vezes; quer 1 (cache)", chamadas)
	}
	if _, found, err := LookupISBN("9780804429573"); err != nil || found {
		t.Fatalf("ISBN inexistente: found=%v err=%v", found, err)
	}
}

func TestBaixaCapaSoDeEnderecoPublico(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 6)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/texto" {
			w.Write([]byte("<html>não é imagem</html>"))
			return
		}
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	// httptest escuta em 127.0.0.1: endereço interno, barrado.
	if _, _, err := FetchImage(srv.URL+"/capa.png", 1<<20); err == nil {
		t.Fatal("baixou de endereço interno")
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://x/y.png", "http://169.254.169.254/latest/meta-data", "javascript:alert(1)"} {
		if _, _, err := FetchImage(u, 1<<20); err == nil {
			t.Errorf("aceitou %q", u)
		}
	}

	AllowPrivateImageHosts = true
	defer func() { AllowPrivateImageHosts = false }()
	data, ct, err := FetchImage(srv.URL+"/capa.png", 1<<20)
	if err != nil || ct != "image/png" || len(data) == 0 {
		t.Fatalf("capa válida: %v %q", err, ct)
	}
	if _, _, err := FetchImage(srv.URL+"/texto", 1<<20); err == nil {
		t.Fatal("aceitou HTML como imagem")
	}
	if _, _, err := FetchImage(srv.URL+"/capa.png", 10); err == nil {
		t.Fatal("aceitou imagem maior que o limite")
	}
}
