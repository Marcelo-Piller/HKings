package models

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

func TestParsePreviewPages(t *testing.T) {
	casos := []struct {
		spec  string
		total int
		quer  []int
		erro  bool
	}{
		{"", 200, []int{1, 2, 3, 4}, false},
		{"", 8, []int{1, 2}, false}, // livro curto: até um quarto
		{"", 2, []int{1}, false},
		{"3-5, 10", 200, []int{3, 4, 5, 10}, false},
		{"10, 3–5, 4", 200, []int{3, 4, 5, 10}, false},
		{"1-11", 200, nil, true}, // passa de 10
		{"1-30", 40, nil, true},  // passa de um quarto
		{"250", 200, nil, true},  // não existe
		{"abc", 200, nil, true},
		{"5-2", 200, nil, true},
	}
	for _, c := range casos {
		got, err := ParsePreviewPages(c.spec, c.total)
		if c.erro {
			if !errors.Is(err, ErrInvalidPreview) {
				t.Errorf("%q/%d: quer erro, veio %v %v", c.spec, c.total, got, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.quer) {
			t.Errorf("%q/%d = %v %v; quer %v", c.spec, c.total, got, err, c.quer)
		}
	}
	if s := FormatPreviewPages([]int{1, 2, 3, 7, 9, 10}); s != "1-3, 7, 9-10" {
		t.Errorf("FormatPreviewPages = %q", s)
	}
}

func TestAmostraPDF(t *testing.T) {
	dir := t.TempDir()
	arquivo := filepath.Join(dir, "livro.pdf")
	doc := fpdf.New("P", "mm", "A5", "")
	doc.SetFont("Helvetica", "", 14)
	for i := 1; i <= 12; i++ {
		doc.AddPage()
		doc.Cell(40, 10, fmt.Sprintf("Pagina %d", i))
	}
	if err := doc.OutputFileAndClose(arquivo); err != nil {
		t.Fatal(err)
	}
	n, err := CountEbookPages(arquivo, EbookPDF)
	if err != nil || n != 12 {
		t.Fatalf("páginas = %d, %v", n, err)
	}
	var buf bytes.Buffer
	if err := WritePDFSample(arquivo, []int{2, 3, 7}, &buf); err != nil {
		t.Fatal(err)
	}
	amostra := filepath.Join(dir, "amostra.pdf")
	os.WriteFile(amostra, buf.Bytes(), 0o600)
	if n, err := CountEbookPages(amostra, EbookPDF); err != nil || n != 3 {
		t.Fatalf("amostra com %d páginas, %v; quer 3", n, err)
	}
}

func epubDeTeste(t *testing.T, capitulos ...string) string {
	t.Helper()
	arquivo := filepath.Join(t.TempDir(), "livro.epub")
	f, _ := os.Create(arquivo)
	zw := zip.NewWriter(f)
	add := func(nome, corpo string) {
		w, _ := zw.Create(nome)
		w.Write([]byte(corpo))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	var manifest, spine strings.Builder
	for i, c := range capitulos {
		fmt.Fprintf(&manifest, `<item id="c%d" href="Text/cap%d.xhtml" media-type="application/xhtml+xml"/>`, i, i)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
		add(fmt.Sprintf("OEBPS/Text/cap%d.xhtml", i), c)
	}
	add("OEBPS/content.opf", `<?xml version="1.0"?><package><manifest>`+manifest.String()+`</manifest><spine>`+spine.String()+`</spine></package>`)
	zw.Close()
	f.Close()
	return arquivo
}

func TestAmostraEPUB(t *testing.T) {
	paragrafo := strings.Repeat("Era uma vez um livro muito comprido. ", 30) // ~1100 caracteres
	cap1 := `<html><head><title>x</title><style>p{}</style></head><body><h1>Capítulo &amp; um</h1><p>` + paragrafo + `</p><p>` + paragrafo + `</p><script>alert(1)</script></body></html>`
	cap2 := `<html><body><h2>Capítulo dois</h2><p>` + paragrafo + `&nbsp;fim<b>!</b></p></body></html>`
	pages, err := EpubPages(epubDeTeste(t, cap1, cap2))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("páginas = %d", len(pages))
	}
	if !pages[0].Blocks[0].Heading || pages[0].Blocks[0].Text != "Capítulo & um" {
		t.Fatalf("primeiro bloco = %+v", pages[0].Blocks[0])
	}
	var tudo strings.Builder
	for i, p := range pages {
		if p.Number != i+1 {
			t.Fatalf("numeração errada: %d", p.Number)
		}
		size := 0
		for _, b := range p.Blocks {
			size += len(b.Text)
			tudo.WriteString(b.Text + "\n")
		}
		if size > EpubPageChars {
			t.Fatalf("página %d com %d caracteres", p.Number, size)
		}
	}
	if strings.Contains(tudo.String(), "alert") || strings.Contains(tudo.String(), "p{}") || strings.Contains(tudo.String(), "<") {
		t.Fatalf("vazou código do EPUB: %s", tudo.String())
	}
	if !strings.Contains(tudo.String(), "comprido. fim!") {
		t.Fatalf("texto final perdido")
	}
	n, err := CountEbookPages(epubDeTeste(t, cap1), EbookEPUB)
	if err != nil || n < 1 {
		t.Fatalf("contagem EPUB = %d, %v", n, err)
	}
}
