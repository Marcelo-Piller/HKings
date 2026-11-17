package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
)

// previewProduct devolve o e-book com amostra que qualquer visitante pode
// ler: anunciado, de vendedor aprovado para e-books e com páginas contadas.
func previewProduct(id int) (*models.Product, bool) {
	p, err := models.GetProduct(id)
	if err != nil || !p.Active || !p.HasPreview() || !models.ValidEbookFileName(p.EbookFile) || !models.GetDigitalRights(p.SellerID).Approved() {
		return nil, false
	}
	return p, true
}

// ebookPreviewData monta a amostra para a página do livro. Em EPUB vem o
// texto das páginas; em PDF, só o endereço do PDF da amostra (EbookSample).
func ebookPreviewData(p *models.Product) map[string]any {
	if _, ok := previewProduct(p.ID); !ok {
		return nil
	}
	pages := p.PreviewPageList()
	data := map[string]any{"Rotulo": p.PreviewLabel(), "Total": p.EbookPages, "Tipo": p.EbookType}
	if p.EbookType == models.EbookPDF {
		data["URL"] = fmt.Sprintf("/livro/amostra?id=%d&v=%s", p.ID, previewVersion(p, pages))
		return data
	}
	if models.IsComicType(p.EbookType) {
		v := previewVersion(p, pages)
		var imgs []map[string]any
		for _, n := range pages {
			imgs = append(imgs, map[string]any{"N": n, "URL": fmt.Sprintf("/livro/amostra/pagina?id=%d&p=%d&v=%s", p.ID, n, v)})
		}
		data["Imagens"] = imgs
		return data
	}
	all, err := models.EpubPages(filepath.Join(ebookDir(), p.EbookFile))
	if err != nil {
		log.Printf("amostra do e-book %d: %v", p.ID, err)
		return nil
	}
	var sel []models.EpubPage
	for _, n := range pages {
		if n >= 1 && n <= len(all) {
			sel = append(sel, all[n-1])
		}
	}
	if len(sel) == 0 {
		return nil
	}
	data["Paginas"] = sel
	return data
}

// previewVersion muda quando o arquivo ou as páginas mudam, para o navegador
// não mostrar uma amostra antiga do cache.
func previewVersion(p *models.Product, pages []int) string {
	sum := sha256.Sum256([]byte(p.EbookFile + "|" + models.FormatPreviewPages(pages)))
	return hex.EncodeToString(sum[:6])
}

// EbookSample - GET /livro/amostra?id=N: PDF só com as páginas da amostra.
// O arquivo completo nunca sai daqui; a amostra fica guardada em disco.
func EbookSample(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	p, ok := previewProduct(id)
	if !ok || p.EbookType != models.EbookPDF {
		http.NotFound(w, r)
		return
	}
	pages := p.PreviewPageList()
	dir := filepath.Join(ebookDir(), "amostras")
	cached := filepath.Join(dir, fmt.Sprintf("%d-%s.pdf", p.ID, previewVersion(p, pages)))
	data, err := os.ReadFile(cached)
	if err != nil {
		var buf bytes.Buffer
		if err := models.WritePDFSample(filepath.Join(ebookDir(), p.EbookFile), pages, &buf); err != nil {
			log.Printf("amostra do e-book %d: %v", p.ID, err)
			http.Error(w, "amostra indisponível", http.StatusInternalServerError)
			return
		}
		data = buf.Bytes()
		if err := os.MkdirAll(dir, 0o750); err == nil {
			tmp := cached + ".tmp"
			if os.WriteFile(tmp, data, 0o640) == nil {
				os.Rename(tmp, cached)
			}
		}
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="amostra.pdf"`)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "amostra.pdf", time.Time{}, bytes.NewReader(data))
}

// BackfillEbookPages conta as páginas dos e-books enviados antes da amostra
// existir, para eles ganharem a amostra sem o vendedor reenviar o arquivo.
func BackfillEbookPages() {
	items, err := models.EbooksWithoutPageCount()
	if err != nil {
		log.Printf("amostras: %v", err)
		return
	}
	for _, it := range items {
		if !models.ValidEbookFileName(it.File) {
			continue
		}
		n := countEbookPages(it.File, it.Kind)
		if n == 0 {
			n = -1 // ilegível: não tenta de novo a cada subida
		}
		if err := models.SetEbookPages(it.ID, n); err != nil {
			log.Printf("amostras: livro %d: %v", it.ID, err)
		}
	}
	if len(items) > 0 {
		log.Printf("amostras: páginas contadas em %d e-book(s)", len(items))
	}
}

// ComicSamplePage - GET /livro/amostra/pagina?id=N&p=K: imagem da página K
// de um quadrinho, só se K estiver na amostra.
func ComicSamplePage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	n, _ := strconv.Atoi(r.URL.Query().Get("p"))
	p, ok := previewProduct(id)
	if !ok || !models.IsComicType(p.EbookType) {
		http.NotFound(w, r)
		return
	}
	pages := p.PreviewPageList()
	inSample := false
	for _, k := range pages {
		if k == n {
			inSample = true
			break
		}
	}
	if !inSample {
		http.NotFound(w, r)
		return
	}
	dir := filepath.Join(ebookDir(), "amostras")
	cached := filepath.Join(dir, fmt.Sprintf("%d-%s-%d.img", p.ID, previewVersion(p, pages), n))
	data, err := os.ReadFile(cached)
	if err != nil {
		data, err = models.ReadComicPage(filepath.Join(ebookDir(), p.EbookFile), p.EbookType, n)
		if err != nil {
			log.Printf("amostra do quadrinho %d, página %d: %v", p.ID, n, err)
			http.Error(w, "página indisponível", http.StatusInternalServerError)
			return
		}
		if err := os.MkdirAll(dir, 0o750); err == nil {
			tmp := cached + ".tmp"
			if os.WriteFile(tmp, data, 0o640) == nil {
				os.Rename(tmp, cached)
			}
		}
	}
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
