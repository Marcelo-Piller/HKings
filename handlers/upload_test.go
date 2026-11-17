package handlers

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngMinimo é um PNG válido de 1x1 pixel, suficiente para a detecção de tipo.
var pngMinimo = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func requisicaoComArquivo(t *testing.T, nomeArquivo string, conteudo []byte) *http.Request {
	t.Helper()
	var corpo bytes.Buffer
	writer := multipart.NewWriter(&corpo)
	parte, err := writer.CreateFormFile("imagem", nomeArquivo)
	if err != nil {
		t.Fatal(err)
	}
	parte.Write(conteudo)
	writer.Close()

	r := httptest.NewRequest(http.MethodPost, "/vendedor/produtos/novo", &corpo)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		t.Fatal(err)
	}
	return r
}

// emDiretorioTemporario roda o teste a partir de um diretório descartável, já
// que o upload grava em static/img/uploads relativo ao diretório atual.
func emDiretorioTemporario(t *testing.T) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(original) })
}

func TestSaveImageUploadAceitaImagemValida(t *testing.T) {
	emDiretorioTemporario(t)

	r := requisicaoComArquivo(t, "foto.png", pngMinimo)
	caminho, err := saveImageUpload(r, "imagem")
	if err != nil {
		t.Fatalf("imagem válida recusada: %v", err)
	}
	if !strings.HasPrefix(caminho, "/static/img/uploads/") {
		t.Fatalf("caminho público inesperado: %q", caminho)
	}
	if !strings.HasSuffix(caminho, ".png") {
		t.Fatalf("extensão deveria vir do conteúdo detectado: %q", caminho)
	}
	if _, err := os.Stat(filepath.Join("static", "img", "uploads", filepath.Base(caminho))); err != nil {
		t.Fatalf("arquivo não foi gravado: %v", err)
	}
}

func TestSaveImageUploadRecusaArquivoDisfarcado(t *testing.T) {
	emDiretorioTemporario(t)

	// Um executável renomeado para .png não pode passar: o tipo é detectado a
	// partir do conteúdo, não da extensão enviada.
	r := requisicaoComArquivo(t, "malicioso.png", []byte("MZ\x90\x00\x03 isto nao e uma imagem"))
	if _, err := saveImageUpload(r, "imagem"); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("arquivo disfarçado aceito; err = %v", err)
	}
}

func TestSaveImageUploadNaoUsaNomeEnviado(t *testing.T) {
	emDiretorioTemporario(t)

	// O nome original é controlado por quem envia e não deve influenciar o
	// caminho gravado em disco.
	r := requisicaoComArquivo(t, "../../../etc/passwd.png", pngMinimo)
	caminho, err := saveImageUpload(r, "imagem")
	if err != nil {
		t.Fatalf("upload recusado: %v", err)
	}
	if strings.Contains(caminho, "..") || strings.Contains(caminho, "passwd") {
		t.Fatalf("nome enviado influenciou o caminho gravado: %q", caminho)
	}
}

func TestResolveProductImageMantemImagemAtual(t *testing.T) {
	emDiretorioTemporario(t)

	// Formulário sem arquivo novo: a imagem existente precisa ser preservada.
	var corpo bytes.Buffer
	writer := multipart.NewWriter(&corpo)
	writer.WriteField("nome", "Produto")
	writer.Close()

	r := httptest.NewRequest(http.MethodPost, "/vendedor/produtos/editar", &corpo)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if err := r.ParseMultipartForm(maxImageBytes); err != nil {
		t.Fatal(err)
	}

	caminho, err := resolveProductImage(r, "/static/img/uploads/antiga.png")
	if err != nil {
		t.Fatal(err)
	}
	if caminho != "/static/img/uploads/antiga.png" {
		t.Fatalf("imagem atual foi perdida: %q", caminho)
	}

	// Produto novo sem imagem recebe o placeholder.
	caminho, err = resolveProductImage(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if caminho != placeholderImage {
		t.Fatalf("esperado placeholder, obtido %q", caminho)
	}
}
