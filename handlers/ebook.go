package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vendas/loja/database"
	"github.com/vendas/loja/models"
)

// Livros digitais: envio do arquivo pelo vendedor, biblioteca do cliente e
// download. O arquivo mora em DataDir()/ebooks, fora de static/, então não há
// URL pública para ele: o único caminho é EbookDownload, que confere a compra.

var (
	errNoEbook       = errors.New("nenhum arquivo de e-book enviado")
	errEbookRequired = errors.New("envie o arquivo do e-book (PDF, EPUB, CBR ou CBZ)")
	errEbookRights   = errors.New("confirme que você é o autor ou tem autorização para vender esta obra em formato digital")
)

// downloadLimiter: até 30 downloads por cliente por hora. Baixar o próprio
// livro algumas vezes é normal; centenas indicam conta compartilhada.
var downloadLimiter = newRateLimiter(30, time.Hour, time.Hour)

// ebookUploadRoutes recebem o arquivo do e-book e precisam de mais tempo e
// tamanho que o resto do site (ver largeUploads).
var ebookUploadRoutes = map[string]bool{
	"/vendedor/produtos/novo":   true,
	"/vendedor/produtos/editar": true,
	"/admin/produtos/novo":      true,
	"/admin/produtos/editar":    true,
}

// audioUploadRoutes recebem faixas de audiobook.
var audioUploadRoutes = map[string]bool{"/vendedor/produtos/audio": true, "/admin/produtos/audio": true}

// largeUploads amplia o limite de corpo e o prazo de leitura só nas rotas de
// cadastro de livro. Um PDF de 60 MB numa conexão lenta passa fácil dos 30 s
// do ReadTimeout do servidor; manter o limite curto no resto do site preserva
// a proteção contra conexões que enviam devagar de propósito.
func largeUploads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && ebookUploadRoutes[r.URL.Path]:
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(15 * time.Minute))
			r.Body = http.MaxBytesReader(w, r.Body, models.MaxEbookBytes+maxImageBytes+(1<<20))
		case r.Method == http.MethodPost && audioUploadRoutes[r.URL.Path]:
			// Faixa de audiobook: até 300 MB, que levam bem mais que 30 s.
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(45 * time.Minute))
			r.Body = http.MaxBytesReader(w, r.Body, models.MaxAudioTrackBytes+(1<<20))
		}
		next.ServeHTTP(w, r)
	})
}

func ebookDir() string {
	return filepath.Join(database.DataDir(), "ebooks")
}

// ebookUpload é o arquivo gravado: nome gerado, tipo, tamanho e hash.
type ebookUpload struct {
	Name   string
	Kind   string
	Size   int64
	SHA256 string
}

// saveEbookUpload grava o e-book enviado no campo "ebook".
func saveEbookUpload(r *http.Request) (*ebookUpload, error) {
	name, kind, size, sum, err := writeEbookUpload(r)
	if err != nil {
		return nil, err
	}
	return &ebookUpload{Name: name, Kind: kind, Size: size, SHA256: sum}, nil
}

func writeEbookUpload(r *http.Request) (name, kind string, size int64, sum string, err error) {
	file, header, err := r.FormFile("ebook")
	if err != nil {
		return "", "", 0, "", errNoEbook
	}
	defer file.Close()
	if header.Size > models.MaxEbookBytes {
		return "", "", 0, "", models.ErrInvalidEbook
	}

	head := make([]byte, 64)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", "", 0, "", err
	}
	kind, err = models.DetectEbookType(head[:n])
	if err != nil {
		return "", "", 0, "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", 0, "", err
	}

	if err := os.MkdirAll(ebookDir(), 0o700); err != nil {
		return "", "", 0, "", err
	}
	name, err = randomFileName("." + kind)
	if err != nil {
		return "", "", 0, "", err
	}
	dest := filepath.Join(ebookDir(), name)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", 0, "", err
	}
	// O hash sai na mesma leitura que grava o arquivo.
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(file, models.MaxEbookBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written > models.MaxEbookBytes {
		os.Remove(dest)
		if copyErr == nil && closeErr == nil {
			return "", "", 0, "", models.ErrInvalidEbook
		}
		return "", "", 0, "", errors.Join(copyErr, closeErr)
	}
	return name, kind, written, hex.EncodeToString(h.Sum(nil)), nil
}

// removeEbookFile apaga o arquivo quando nenhum produto aponta mais para ele.
func removeEbookFile(name string) {
	if !models.ValidEbookFileName(name) || models.EbookFileInUse(name) {
		return
	}
	if err := os.Remove(filepath.Join(ebookDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("não foi possível apagar o e-book %s: %v", name, err)
	}
}

// applyBookForm preenche no produto o formato, o autor, o peso e as medidas
// (livro físico) e o arquivo do e-book, iguais nos painéis do admin e do
// vendedor.
//
// current é o produto já gravado (nil num cadastro novo). O formato é
// escolhido só no cadastro. Devolve o arquivo recém-gravado, para quem chama
// apagá-lo se a gravação do produto falhar, e o arquivo substituído, para
// apagá-lo se der certo.
func applyBookForm(r *http.Request, p *models.Product, current *models.Product) (novo, antigo string, err error) {
	p.Author = r.FormValue("autor")
	p.ISBN = r.FormValue("isbn")
	p.Publisher = r.FormValue("editora")
	p.Language = r.FormValue("idioma")
	// Peso e medidas: vazio ou inválido vira zero, e o modelo usa o pacote
	// padrão de livro.
	p.WeightG, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("peso")))
	p.HeightCm, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("altura")))
	p.WidthCm, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("largura")))
	p.LengthCm, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("comprimento")))
	if current != nil {
		p.Format = current.Format
	} else if r.FormValue("formato") == models.FormatDigital {
		p.Format = models.FormatDigital
	} else {
		p.Format = models.FormatPhysical
	}
	if p.Format != models.FormatDigital {
		return "", "", nil
	}

	up, err := saveEbookUpload(r)
	switch {
	case err == nil:
		// A declaração de direitos acompanha cada arquivo enviado.
		if r.FormValue("direitos") != "on" {
			removeEbookFile(up.Name)
			return "", "", errEbookRights
		}
		p.EbookFile, p.EbookType, p.EbookSize, p.EbookSHA256, p.RightsDeclared = up.Name, up.Kind, up.Size, up.SHA256, true
		p.EbookPages = countEbookPages(up.Name, up.Kind)
		if models.IsComicType(up.Kind) && p.EbookPages == 0 {
			// ZIP/RAR sem imagens não é quadrinho.
			removeEbookFile(up.Name)
			return "", "", models.ErrInvalidEbook
		}
		if err := applyPreviewPages(r, p); err != nil {
			removeEbookFile(up.Name)
			return "", "", err
		}
		if current != nil {
			antigo = current.EbookFile
		}
		return up.Name, antigo, nil
	case errors.Is(err, errNoEbook):
		if current == nil || current.EbookFile == "" {
			return "", "", errEbookRequired
		}
		p.EbookFile, p.EbookType, p.EbookSize, p.EbookSHA256 = current.EbookFile, current.EbookType, current.EbookSize, current.EbookSHA256
		p.EbookPages = current.EbookPages
		if p.EbookPages == 0 {
			// E-book enviado antes da amostra existir: conta agora.
			p.EbookPages = countEbookPages(p.EbookFile, p.EbookType)
		}
		return "", "", applyPreviewPages(r, p)
	default:
		return "", "", err
	}
}

// countEbookPages conta as páginas do e-book gravado. Arquivo que não dá
// para ler (PDF protegido, EPUB estranho) fica sem amostra, sem barrar a venda.
func countEbookPages(name, kind string) int {
	n, err := models.CountEbookPages(filepath.Join(ebookDir(), name), kind)
	if err != nil {
		log.Printf("e-book %s: páginas não contadas, sem amostra: %v", name, err)
		return 0
	}
	return n
}

// applyPreviewPages confere a escolha de páginas da amostra ("1-4, 10") e
// grava no formato normalizado. Vazio é a amostra padrão.
func applyPreviewPages(r *http.Request, p *models.Product) error {
	spec := strings.TrimSpace(r.FormValue("amostra"))
	if spec == "" || p.EbookPages <= 0 {
		p.PreviewPages = ""
		return nil
	}
	pages, err := models.ParsePreviewPages(spec, p.EbookPages)
	if err != nil {
		p.PreviewPages = spec // volta ao formulário como foi digitado
		return err
	}
	p.PreviewPages = models.FormatPreviewPages(pages)
	return nil
}

// ebookErrorMessage traduz os erros do envio do e-book para o formulário.
func ebookErrorMessage(err error) string {
	switch {
	case errors.Is(err, errEbookRights), errors.Is(err, errEbookRequired), errors.Is(err, models.ErrInvalidEbook), errors.Is(err, models.ErrInvalidPreview):
		return capitalizeFirst(err.Error()) + "."
	default:
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return capitalizeFirst(models.ErrInvalidEbook.Error()) + "."
		}
		log.Printf("envio de e-book: %v", err)
		return "Não foi possível salvar o arquivo do e-book. Tente de novo."
	}
}

// CustomerLibrary - GET /conta/biblioteca: e-books comprados.
func CustomerLibrary(w http.ResponseWriter, r *http.Request) {
	items, err := models.ListLibrary(currentCustomerID(r))
	if err != nil {
		log.Printf("biblioteca: %v", err)
	}
	data := map[string]any{"Items": items}
	switch r.URL.Query().Get("aviso") {
	case "limite":
		data["Aviso"] = "Você baixou muitos arquivos na última hora. Tente de novo mais tarde."
	case "indisponivel":
		data["Aviso"] = "Este livro não está disponível para download no momento."
	}
	renderStore(w, r, "account_library.html", data)
}

// EbookDownload - GET /conta/biblioteca/baixar?livro=N: entrega o arquivo a
// quem comprou.
func EbookDownload(w http.ResponseWriter, r *http.Request) {
	customerID := currentCustomerID(r)
	productID, _ := strconv.Atoi(r.URL.Query().Get("livro"))

	ebook, err := models.GetEbookForCustomer(customerID, productID)
	if err != nil {
		if !errors.Is(err, models.ErrEbookNotOwned) {
			log.Printf("download do e-book %d: %v", productID, err)
		}
		http.NotFound(w, r)
		return
	}

	key := "download|" + strconv.Itoa(customerID)
	if allowed, _ := downloadLimiter.Allowed(key); !allowed {
		http.Redirect(w, r, "/conta/biblioteca?aviso=limite", http.StatusSeeOther)
		return
	}

	f, err := os.Open(filepath.Join(ebookDir(), ebook.File))
	if err != nil {
		log.Printf("download do e-book %d: arquivo %s: %v", productID, ebook.File, err)
		http.Redirect(w, r, "/conta/biblioteca?aviso=indisponivel", http.StatusSeeOther)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "arquivo indisponível", http.StatusInternalServerError)
		return
	}
	downloadLimiter.RegisterFailure(key) // conta cada download entregue

	// Um livro grande numa conexão lenta passa dos 30 s do WriteTimeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	w.Header().Set("Content-Type", models.EbookContentType(ebook.Type))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": downloadFileName(ebook.Name, ebook.Type)}))
	w.Header().Set("Cache-Control", "private, no-store")
	// ServeContent atende Range: um download interrompido pode continuar.
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// downloadFileName monta o nome do arquivo a partir do título, sem caracteres
// que confundam o sistema de arquivos de quem baixa.
func downloadFileName(title, kind string) string {
	var b strings.Builder
	// Letras e números ficam; todo o resto (barras, aspas, dois-pontos) vira
	// espaço, para "Contos/Vol. 2" não sair "ContosVol2".
	for _, r := range strings.TrimSpace(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	if name == "" {
		name = "livro"
	}
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	return name + "." + kind
}
