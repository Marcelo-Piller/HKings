package models

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/vendas/loja/database"
)

// Amostra de leitura do e-book: algumas páginas que qualquer visitante lê na
// página do livro. Por padrão são as 4 primeiras; o vendedor pode escolher
// outras ("1-3, 10"), até um limite que impede mostrar o livro inteiro.
//
// Em PDF a página é a do arquivo. EPUB não tem página fixa: o texto é
// dividido em páginas de tamanho de livro impresso (EpubPageChars).

// DefaultPreviewPages é quantas páginas a amostra mostra sem escolha do vendedor.
const DefaultPreviewPages = 4

// MaxPreviewPages é o teto absoluto da amostra, em qualquer livro.
const MaxPreviewPages = 10

// EpubPageChars é o tamanho aproximado de uma página de EPUB, em caracteres
// (uma página de livro de bolso).
const EpubPageChars = 1800

// ErrInvalidPreview: a escolha de páginas da amostra não vale para o livro.
var ErrInvalidPreview = errors.New("páginas da amostra inválidas")

// PreviewLimit é o máximo de páginas que a amostra pode ter num livro de
// total páginas: um quarto do livro, entre 1 e MaxPreviewPages.
func PreviewLimit(total int) int {
	limit := total / 4
	if limit > MaxPreviewPages {
		limit = MaxPreviewPages
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// ParsePreviewPages lê a escolha do vendedor ("1-4, 10") e confere com o
// livro: páginas existentes, sem passar do limite. Vazio é a amostra padrão.
// Devolve as páginas em ordem e sem repetição.
func ParsePreviewPages(spec string, total int) ([]int, error) {
	if total <= 0 {
		return nil, nil
	}
	limit := PreviewLimit(total)
	spec = strings.TrimSpace(spec)
	if spec == "" {
		n := DefaultPreviewPages
		if n > limit {
			n = limit
		}
		pages := make([]int, n)
		for i := range pages {
			pages[i] = i + 1
		}
		return pages, nil
	}
	seen := map[int]bool{}
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to := part, part
		if i := strings.IndexAny(part, "-–"); i >= 0 {
			from, to = strings.TrimSpace(part[:i]), strings.TrimSpace(strings.TrimLeft(part[i:], "-–"))
		}
		a, errA := strconv.Atoi(from)
		b, errB := strconv.Atoi(to)
		if errA != nil || errB != nil || a < 1 || b < a {
			return nil, fmt.Errorf("%w: use números e intervalos, como \"1-4, 10\"", ErrInvalidPreview)
		}
		if b > total {
			return nil, fmt.Errorf("%w: o livro tem %s", ErrInvalidPreview, pagesLabel(total))
		}
		if b-a >= limit {
			return nil, fmt.Errorf("%w: a amostra deste livro pode ter no máximo %s", ErrInvalidPreview, pagesLabel(limit))
		}
		for p := a; p <= b; p++ {
			seen[p] = true
		}
	}
	if len(seen) == 0 {
		return ParsePreviewPages("", total)
	}
	if len(seen) > limit {
		return nil, fmt.Errorf("%w: a amostra deste livro pode ter no máximo %s", ErrInvalidPreview, pagesLabel(limit))
	}
	pages := make([]int, 0, len(seen))
	for p := range seen {
		pages = append(pages, p)
	}
	sort.Ints(pages)
	return pages, nil
}

// FormatPreviewPages escreve as páginas como o vendedor digitaria: "1-4, 10".
func FormatPreviewPages(pages []int) string {
	var parts []string
	for i := 0; i < len(pages); {
		j := i
		for j+1 < len(pages) && pages[j+1] == pages[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d-%d", pages[i], pages[j]))
		} else {
			parts = append(parts, strconv.Itoa(pages[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// PreviewPageList são as páginas da amostra deste e-book; vazio quando o
// livro não tem amostra (arquivo sem páginas contadas).
func (p Product) PreviewPageList() []int {
	pages, err := ParsePreviewPages(p.PreviewPages, p.EbookPages)
	if err != nil {
		// Escolha antiga que deixou de valer (arquivo trocado): volta ao padrão.
		pages, _ = ParsePreviewPages("", p.EbookPages)
	}
	return pages
}

// PreviewMax é o limite de páginas da amostra deste livro.
func (p Product) PreviewMax() int { return PreviewLimit(p.EbookPages) }

// HasPreview diz se o livro tem amostra de leitura.
func (p Product) HasPreview() bool { return p.IsDigital() && p.EbookPages > 0 && p.EbookFile != "" }

// PreviewLabel descreve a amostra para a página do livro: "páginas 1-4".
func (p Product) PreviewLabel() string {
	pages := p.PreviewPageList()
	if len(pages) == 1 {
		return "página " + FormatPreviewPages(pages)
	}
	return "páginas " + FormatPreviewPages(pages)
}

// CountEbookPages conta as páginas do arquivo. Zero sem erro não acontece:
// arquivo sem páginas legíveis devolve erro, e o livro fica sem amostra.
func CountEbookPages(file, kind string) (int, error) {
	switch kind {
	case EbookPDF:
		f, err := os.Open(file)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return api.PageCount(context.Background(), f, pdfConfig())
	case EbookEPUB:
		pages, err := EpubPages(file)
		return len(pages), err
	case EbookCBR, EbookCBZ:
		pages, err := ComicPages(file, kind)
		return len(pages), err
	}
	return 0, ErrInvalidEbook
}

func pdfConfig() *pdfmodel.Configuration {
	// Sem arquivo de configuração: o container não tem pasta pessoal.
	return pdfmodel.NewStatelessConfiguration()
}

// WritePDFSample grava em w um PDF só com as páginas pedidas do arquivo.
func WritePDFSample(file string, pages []int, w io.Writer) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	sel := make([]string, len(pages))
	for i, p := range pages {
		sel[i] = strconv.Itoa(p)
	}
	return api.Trim(context.Background(), f, w, sel, pdfConfig())
}

// EpubBlock é um parágrafo do EPUB; Heading marca os títulos.
type EpubBlock struct {
	Text    string
	Heading bool
}

// EpubPage é uma página de texto do EPUB.
type EpubPage struct {
	Number int
	Blocks []EpubBlock
}

var epubCache = struct {
	sync.Mutex
	m map[string][]EpubPage
}{m: map[string][]EpubPage{}}

// EpubPages lê o EPUB na ordem de leitura e divide o texto em páginas. Só
// texto: nada do HTML do arquivo chega à página da loja.
func EpubPages(file string) ([]EpubPage, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s|%d|%d", file, st.Size(), st.ModTime().UnixNano())
	epubCache.Lock()
	if pages, ok := epubCache.m[key]; ok {
		epubCache.Unlock()
		return pages, nil
	}
	epubCache.Unlock()

	blocks, err := epubBlocks(file)
	if err != nil {
		return nil, err
	}
	pages := paginate(blocks)
	if len(pages) == 0 {
		return nil, fmt.Errorf("EPUB sem texto legível")
	}
	epubCache.Lock()
	if len(epubCache.m) > 64 {
		epubCache.m = map[string][]EpubPage{}
	}
	epubCache.m[key] = pages
	epubCache.Unlock()
	return pages, nil
}

// limites de leitura do EPUB, contra arquivo malicioso (zip bomb).
const (
	maxEpubEntry = 8 << 20
	maxEpubText  = 20 << 20
)

func epubBlocks(file string) ([]EpubBlock, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("EPUB sem %s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxEpubEntry+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxEpubEntry {
			return nil, fmt.Errorf("EPUB com parte grande demais: %s", name)
		}
		return data, nil
	}

	// container.xml aponta o arquivo OPF, que tem a ordem de leitura (spine).
	data, err := read("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var container struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(data, &container); err != nil || len(container.Rootfiles) == 0 {
		return nil, fmt.Errorf("EPUB sem container válido")
	}
	opfPath := container.Rootfiles[0].Path
	data, err = read(opfPath)
	if err != nil {
		return nil, err
	}
	var opf struct {
		Items []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	if err := xml.Unmarshal(data, &opf); err != nil {
		return nil, fmt.Errorf("EPUB com OPF inválido")
	}
	hrefs := map[string]string{}
	for _, it := range opf.Items {
		hrefs[it.ID] = it.Href
	}
	base := path.Dir(opfPath)
	var blocks []EpubBlock
	total := 0
	for _, ref := range opf.Spine {
		href, ok := hrefs[ref.IDRef]
		if !ok {
			continue
		}
		if i := strings.IndexByte(href, '#'); i >= 0 {
			href = href[:i]
		}
		name := path.Clean(path.Join(base, href))
		if dec, err := urlUnescape(name); err == nil {
			name = dec
		}
		doc, err := read(name)
		if err != nil {
			continue
		}
		for _, b := range xhtmlBlocks(doc) {
			total += len(b.Text)
			if total > maxEpubText {
				return blocks, nil
			}
			blocks = append(blocks, b)
		}
	}
	return blocks, nil
}

func urlUnescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", err
			}
			b.WriteByte(byte(v))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

var epubBlockTags = map[string]bool{
	"p": true, "div": true, "li": true, "blockquote": true, "br": true, "tr": true, "section": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "pre": true, "dd": true, "dt": true,
}

var epubHeadingTags = map[string]bool{"h1": true, "h2": true, "h3": true}

// xhtmlBlocks extrai os parágrafos de um capítulo em XHTML.
func xhtmlBlocks(doc []byte) []EpubBlock {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	var (
		blocks  []EpubBlock
		cur     strings.Builder
		skip    int // dentro de head, script, style
		heading int
		inBody  bool
	)
	flush := func() {
		t := strings.Join(strings.Fields(cur.String()), " ")
		cur.Reset()
		if t != "" {
			blocks = append(blocks, EpubBlock{Text: t, Heading: heading > 0})
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			switch {
			case name == "body":
				inBody = true
			case name == "head" || name == "script" || name == "style":
				skip++
			case epubBlockTags[name]:
				flush()
				if epubHeadingTags[name] {
					heading++
				}
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			switch {
			case name == "head" || name == "script" || name == "style":
				if skip > 0 {
					skip--
				}
			case epubBlockTags[name]:
				flush()
				if epubHeadingTags[name] && heading > 0 {
					heading--
				}
			}
		case xml.CharData:
			if skip == 0 && inBody {
				cur.Write(t)
			}
		}
	}
	flush()
	return blocks
}

// paginate junta os parágrafos em páginas de cerca de EpubPageChars
// caracteres; parágrafo maior que uma página é quebrado entre palavras.
func paginate(blocks []EpubBlock) []EpubPage {
	var pages []EpubPage
	var cur []EpubBlock
	size := 0
	push := func() {
		if len(cur) > 0 {
			pages = append(pages, EpubPage{Number: len(pages) + 1, Blocks: cur})
			cur, size = nil, 0
		}
	}
	for _, b := range blocks {
		// Título começa página nova quando a atual já tem texto razoável.
		if b.Heading && size > EpubPageChars/2 {
			push()
		}
		text := b.Text
		for len(text) > 0 {
			room := EpubPageChars - size
			if len(text) <= room {
				cur = append(cur, EpubBlock{Text: text, Heading: b.Heading})
				size += len(text)
				break
			}
			cut := strings.LastIndexByte(text[:room], ' ')
			if cut <= 0 {
				if size > 0 {
					push()
					continue
				}
				cut = room
				for cut < len(text) && text[cut] != ' ' {
					cut++
				}
			}
			cur = append(cur, EpubBlock{Text: text[:cut], Heading: b.Heading})
			push()
			text = strings.TrimLeft(text[cut:], " ")
		}
	}
	push()
	return pages
}

// pagesLabel escreve "1 página" ou "N páginas".
func pagesLabel(n int) string {
	if n == 1 {
		return "1 página"
	}
	return strconv.Itoa(n) + " páginas"
}

// EbookRef identifica o arquivo de um e-book.
type EbookRef struct {
	ID   int
	File string
	Kind string
}

// EbooksWithoutPageCount lista os e-books com páginas ainda não contadas.
func EbooksWithoutPageCount() ([]EbookRef, error) {
	rows, err := database.DB.Query(`SELECT id, ebook_file, ebook_type FROM products
		WHERE product_format='digital' AND ebook_pages=0 AND ebook_file<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EbookRef
	for rows.Next() {
		var r EbookRef
		if err := rows.Scan(&r.ID, &r.File, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEbookPages grava o número de páginas do e-book (-1 = ilegível).
func SetEbookPages(id, pages int) error {
	_, err := database.DB.Exec(`UPDATE products SET ebook_pages=? WHERE id=?`, pages, id)
	return err
}
