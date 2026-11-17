package handlers

import (
	"archive/zip"
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vendas/loja/models"
)

func requisicaoComEbook(t *testing.T, campo, nome string, conteudo []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(campo, nome)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(conteudo)
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/vendedor/produtos/novo", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEnvioDeEbookGravaForaDaPastaPublica(t *testing.T) {
	original, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original) })

	var epub bytes.Buffer
	zw := zip.NewWriter(&epub)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte("application/epub+zip"))
	zw.Close()

	up, err := saveEbookUpload(requisicaoComEbook(t, "ebook", "meu-livro.epub", epub.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	nome, tipo, tamanho := up.Name, up.Kind, up.Size
	if len(up.SHA256) != 64 {
		t.Fatalf("hash do arquivo = %q", up.SHA256)
	}
	if tipo != models.EbookEPUB || tamanho != int64(epub.Len()) || !models.ValidEbookFileName(nome) {
		t.Fatalf("nome=%q tipo=%q tamanho=%d", nome, tipo, tamanho)
	}
	if _, err := os.Stat(filepath.Join(ebookDir(), nome)); err != nil {
		t.Fatalf("arquivo não gravado: %v", err)
	}
	if filepath.Base(filepath.Dir(filepath.Join(ebookDir(), nome))) != "ebooks" {
		t.Fatal("e-book gravado fora da pasta privada")
	}

	// Texto renomeado para .pdf não passa, e nada fica no disco.
	_, err = saveEbookUpload(requisicaoComEbook(t, "ebook", "livro.pdf", []byte("isto não é um PDF")))
	if !errors.Is(err, models.ErrInvalidEbook) {
		t.Fatalf("arquivo falso aceito: %v", err)
	}
	if arquivos, _ := os.ReadDir(ebookDir()); len(arquivos) != 1 {
		t.Fatalf("sobrou arquivo da tentativa recusada: %d arquivos", len(arquivos))
	}

	if _, err := saveEbookUpload(requisicaoComEbook(t, "imagem", "capa.png", []byte("x"))); !errors.Is(err, errNoEbook) {
		t.Fatalf("sem campo ebook: %v", err)
	}
}

func TestVoltaDepoisDoLoginSoParaOProprioSite(t *testing.T) {
	validos := []string{"/checkout", "/produto?id=3", "/conta/biblioteca"}
	for _, v := range validos {
		if safeReturnPath(v) != v {
			t.Errorf("recusou %q", v)
		}
	}
	// Endereços que o navegador lê como outro site.
	for _, v := range []string{"", "https://golpe.com", "//golpe.com", "/\\golpe.com", "golpe.com", "/ok\r\nSet-Cookie: x=1"} {
		if safeReturnPath(v) != "" {
			t.Errorf("aceitou %q", v)
		}
	}
}

func TestNomeDoArquivoBaixado(t *testing.T) {
	casos := map[string]string{
		"Memórias Póstumas de Brás Cubas": "Memórias Póstumas de Brás Cubas.pdf",
		"../../etc/passwd":                "etc passwd.pdf",
		`Contos "do" Sertão: vol. 2`:      "Contos do Sertão vol 2.pdf",
		"???":                             "livro.pdf",
	}
	for titulo, quer := range casos {
		if got := downloadFileName(titulo, "pdf"); got != quer {
			t.Errorf("downloadFileName(%q) = %q; quer %q", titulo, got, quer)
		}
	}
}
