package models

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/vendas/loja/database"
)

// Livros digitais (e-books).
//
// O arquivo é guardado fora de static/, com nome aleatório, e só sai pelo
// download de quem comprou (handlers/ebook.go). Cada compra paga dá acesso ao
// arquivo atual do livro: se o autor corrigir o e-book, quem já comprou baixa
// a versão nova.

const (
	FormatPhysical = "fisico"
	FormatDigital  = "digital"

	EbookPDF  = "pdf"
	EbookEPUB = "epub"
	// Quadrinhos: pacote de imagens em RAR (CBR) ou ZIP (CBZ).
	EbookCBR = "cbr"
	EbookCBZ = "cbz"

	// MaxEbookBytes limita o arquivo enviado. Um romance em EPUB costuma ter
	// menos de 5 MB; PDFs com ilustrações passam de 20 MB.
	MaxEbookBytes = 60 << 20
)

var (
	// ErrInvalidEbook: o arquivo não é um PDF, EPUB ou quadrinho de verdade.
	ErrInvalidEbook = errors.New("envie um arquivo PDF, EPUB, CBR ou CBZ de até 60 MB")
	// ErrDigitalNeedsAccount: e-book fica na biblioteca da conta, então a
	// compra exige estar logado.
	ErrDigitalNeedsAccount = errors.New("livro digital exige uma conta")
	// ErrAlreadyOwned: o cliente já comprou este e-book.
	ErrAlreadyOwned = errors.New("você já tem este livro digital")
	// ErrEbookNotOwned: download pedido por quem não comprou o livro.
	ErrEbookNotOwned = errors.New("livro digital não encontrado na sua biblioteca")
)

var ebookFileName = regexp.MustCompile(`^[0-9a-f]{32}\.(pdf|epub|cbr|cbz)$`)

// ValidEbookFileName aceita só nomes gerados pela loja: sem barras nem "..",
// o nome não tem como apontar para fora da pasta dos e-books.
func ValidEbookFileName(name string) bool {
	return ebookFileName.MatchString(name)
}

func ValidEbookType(t string) bool {
	return t == EbookPDF || t == EbookEPUB || t == EbookCBR || t == EbookCBZ
}

// DetectEbookType reconhece o arquivo pelo conteúdo, não pelo nome: renomear
// um executável para .pdf não o transforma em PDF.
//
// PDF começa com "%PDF-". EPUB é um ZIP cujo primeiro arquivo, sem
// compressão, se chama "mimetype" e contém "application/epub+zip" — é isso
// que os leitores de e-book conferem.
func DetectEbookType(head []byte) (string, error) {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return EbookPDF, nil
	case len(head) >= 58 && bytes.HasPrefix(head, []byte("PK\x03\x04")) &&
		string(head[30:38]) == "mimetype" &&
		bytes.HasPrefix(head[38:], []byte("application/epub+zip")):
		return EbookEPUB, nil
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07")):
		// RAR 4 ("Rar!\x1a\x07\x00") ou RAR 5 ("Rar!\x1a\x07\x01\x00").
		return EbookCBR, nil
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		// ZIP que não é EPUB: quadrinho em CBZ. Se não tiver imagens, o
		// envio é recusado depois, na contagem das páginas.
		return EbookCBZ, nil
	}
	return "", ErrInvalidEbook
}

// EbookContentType é o tipo enviado no download.
func EbookContentType(t string) string {
	switch t {
	case EbookEPUB:
		return "application/epub+zip"
	case EbookCBR:
		return "application/vnd.comicbook-rar"
	case EbookCBZ:
		return "application/vnd.comicbook+zip"
	}
	return "application/pdf"
}

// ── Exibição ─────────────────────────────────────────────────────────────────

// IsDigital indica e-book.
func (p Product) IsDigital() bool { return p.Format == FormatDigital }

// FormatLabel descreve o formato para quem compra.
func (p Product) FormatLabel() string {
	if p.IsDigital() {
		return "E-book"
	}
	return "Livro físico"
}

// EbookTypeLabel devolve "PDF" ou "EPUB".
func (p Product) EbookTypeLabel() string { return strings.ToUpper(p.EbookType) }

// EbookSizeLabel devolve o tamanho do arquivo no padrão brasileiro: "2,4 MB".
func (p Product) EbookSizeLabel() string { return fileSizeLabel(p.EbookSize) }

// ShelfLabel é a etiqueta da vitrine: "Novo", "Usado" ou "E-book".
func (p Product) ShelfLabel() string {
	if p.IsDigital() {
		return "E-book"
	}
	return p.ConditionLabel()
}

// Available diz se o livro pode ir para a sacola: e-book não tem estoque.
func (p Product) Available() bool {
	return p.IsDigital() || p.Stock > 0
}

func fileSizeLabel(size int64) string {
	switch {
	case size <= 0:
		return ""
	case size < 1<<20:
		return fmt.Sprintf("%d KB", (size+1023)/1024)
	default:
		return strings.Replace(fmt.Sprintf("%.1f MB", float64(size)/(1<<20)), ".", ",", 1)
	}
}

// ── Posse e biblioteca ───────────────────────────────────────────────────────

// paidStatuses são os status de pedido em que o pagamento foi confirmado.
const paidStatuses = `('pago','pronto','enviado','entregue')`

// CustomerOwnsProduct diz se o cliente tem um pedido pago que dá acesso ao
// e-book (edição e-book ou combo; audiobook sozinho não conta).
func CustomerOwnsProduct(customerID, productID int) bool {
	return customerOwns(customerID, productID, EditionEbook)
}

// LibraryItem é um e-book na biblioteca do cliente.
type LibraryItem struct {
	ProductID int
	Name      string
	Author    string
	ImageURL  string
	EbookType string
	EbookSize int64
	OrderID   int
	PaidAt    string
	// O que foi comprado: o e-book, o audiobook ou os dois (combo).
	OwnsEbook   bool
	OwnsAudio   bool
	AudioTracks int
	AudioSecs   int
	// Available é falso quando o arquivo foi removido (ex.: denúncia de
	// pirataria atendida); o livro continua listado para o cliente saber.
	Available bool
}

func (l LibraryItem) EbookTypeLabel() string { return strings.ToUpper(l.EbookType) }
func (l LibraryItem) EbookSizeLabel() string { return fileSizeLabel(l.EbookSize) }
func (l LibraryItem) AudioLabel() string     { return DurationLabel(l.AudioSecs) }

// ListLibrary lista os e-books comprados pelo cliente, um por livro, do mais
// recente para o mais antigo. Inclui livros que saíram da loja: quem comprou
// continua podendo baixar.
func ListLibrary(customerID int) ([]LibraryItem, error) {
	rows, err := database.DB.Query(`
		SELECT p.id, p.name, p.author, p.image_url, p.ebook_type, p.ebook_size, MAX(o.id), COALESCE(MAX(o.paid_at), ''),
			CASE WHEN p.ebook_file <> '' THEN 1 ELSE 0 END,
			MAX(CASE WHEN oi.edition IN ('', 'ebook', 'combo') THEN 1 ELSE 0 END),
			MAX(CASE WHEN oi.edition IN ('audio', 'combo') THEN 1 ELSE 0 END),
			(SELECT COUNT(*) FROM audio_tracks at WHERE at.product_id = p.id),
			(SELECT COALESCE(SUM(at.duration_sec), 0) FROM audio_tracks at WHERE at.product_id = p.id)
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		JOIN products p ON p.id = oi.product_id
		WHERE o.customer_id=? AND o.status IN `+paidStatuses+` AND p.product_format='digital'
		GROUP BY p.id
		ORDER BY MAX(o.id) DESC`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []LibraryItem
	for rows.Next() {
		var it LibraryItem
		var available, ebook, audio int
		if err := rows.Scan(&it.ProductID, &it.Name, &it.Author, &it.ImageURL, &it.EbookType, &it.EbookSize,
			&it.OrderID, &it.PaidAt, &available, &ebook, &audio, &it.AudioTracks, &it.AudioSecs); err != nil {
			return nil, err
		}
		it.Available = available == 1
		it.OwnsEbook, it.OwnsAudio = ebook == 1, audio == 1
		items = append(items, it)
	}
	return items, rows.Err()
}

// EbookDownload é o que o handler precisa para entregar o arquivo.
type EbookDownload struct {
	ProductID int
	Name      string
	File      string
	Type      string
}

// GetEbookForCustomer confere a posse e devolve o arquivo do e-book. Só quem
// tem pedido pago com o livro passa daqui.
func GetEbookForCustomer(customerID, productID int) (*EbookDownload, error) {
	if !CustomerOwnsProduct(customerID, productID) {
		return nil, ErrEbookNotOwned
	}
	var d EbookDownload
	err := database.DB.QueryRow(`SELECT id, name, ebook_file, ebook_type FROM products
		WHERE id=? AND product_format='digital'`, productID).Scan(&d.ProductID, &d.Name, &d.File, &d.Type)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !ValidEbookFileName(d.File)) {
		return nil, ErrEbookNotOwned
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// EbookFileInUse diz se algum produto ainda aponta para o arquivo, antes de
// apagá-lo do disco.
func EbookFileInUse(name string) bool {
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM products WHERE ebook_file=?`, name).Scan(&n); err != nil {
		return true
	}
	return n > 0
}
