package models

import (
	"archive/zip"
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestQuadrinhoCBR(t *testing.T) {
	arquivo := filepath.Join("testdata", "quadrinho.cbr")
	paginas, err := ComicPages(arquivo, EbookCBR)
	if err != nil {
		t.Fatal(err)
	}
	// Ordem natural (pag2 antes de pag10) e só imagens (sem o leia-me.txt).
	quer := []string{"HQ/pag1.png", "HQ/pag2.png", "HQ/pag10.png"}
	if !reflect.DeepEqual(paginas, quer) {
		t.Fatalf("páginas = %v; quer %v", paginas, quer)
	}
	n, err := CountEbookPages(arquivo, EbookCBR)
	if err != nil || n != 3 {
		t.Fatalf("contagem = %d, %v", n, err)
	}
	img, err := ReadComicPage(arquivo, EbookCBR, 2)
	if err != nil {
		t.Fatal(err)
	}
	m, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, _, _ := m.At(5, 5).RGBA(); g>>8 != 160 || r>>8 != 40 {
		t.Fatalf("página 2 errada: r=%d g=%d", r>>8, g>>8)
	}
	if _, err := ReadComicPage(arquivo, EbookCBR, 4); err == nil {
		t.Fatal("página inexistente lida")
	}
}

func TestQuadrinhoCBZ(t *testing.T) {
	dir := t.TempDir()
	cbz := filepath.Join(dir, "hq.cbz")
	f, _ := os.Create(cbz)
	zw := zip.NewWriter(f)
	for _, nome := range []string{"10.jpg", "2.jpg", "1.jpg", "__MACOSX/._1.jpg", "creditos.txt"} {
		w, _ := zw.Create(nome)
		w.Write([]byte("imagem " + nome))
	}
	zw.Close()
	f.Close()
	paginas, err := ComicPages(cbz, EbookCBZ)
	if err != nil || !reflect.DeepEqual(paginas, []string{"1.jpg", "2.jpg", "10.jpg"}) {
		t.Fatalf("páginas = %v, %v", paginas, err)
	}
	if img, err := ReadComicPage(cbz, EbookCBZ, 3); err != nil || string(img) != "imagem 10.jpg" {
		t.Fatalf("página 3 = %q, %v", img, err)
	}

	// ZIP sem imagens não é quadrinho.
	vazio := filepath.Join(dir, "vazio.cbz")
	f, _ = os.Create(vazio)
	zw = zip.NewWriter(f)
	w, _ := zw.Create("leia-me.txt")
	w.Write([]byte("oi"))
	zw.Close()
	f.Close()
	if _, err := CountEbookPages(vazio, EbookCBZ); err == nil {
		t.Fatal("ZIP sem imagens aceito como quadrinho")
	}
}

func TestOrdemNatural(t *testing.T) {
	if !naturalLess("cap2/p9.jpg", "cap2/p10.jpg") || naturalLess("p10", "p9") || !naturalLess("A1", "b1") {
		t.Fatal("ordem natural errada")
	}
}
