package models

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vendas/loja/database"
)

// Autorização para vender livros digitais (contra pirataria).
//
// Livro físico qualquer vendedor anuncia. E-book, não: só quem o admin
// aprovou como autor(a) independente ou como editora/distribuidora parceira.
//   - Autor(a): o e-book só é aceito se o autor do livro for um dos nomes
//     aprovados. Quem publica como "Ana Souza" não sobe "Turma da Mônica".
//   - Editora parceira: publica vários autores, sob contrato conferido pelo
//     admin na aprovação.
// Além disso, o mesmo arquivo (pelo hash) não pode estar à venda duas vezes,
// o que barra o PDF pirata que circula na internet. A loja (seller 0) não
// precisa de aprovação.

const (
	DigitalPending  = "pendente"
	DigitalApproved = "aprovado"
	DigitalRejected = "recusado"

	DigitalKindAuthor    = "autor"
	DigitalKindPublisher = "editora"

	maxDigitalAuthors = 5
)

var (
	ErrDigitalRequest     = errors.New("pedido de venda de e-books inválido")
	ErrDigitalNotAllowed  = errors.New("venda de e-books ainda não aprovada para este vendedor")
	ErrDigitalUnavailable = errors.New("este e-book não está mais à venda")
)

// DigitalRights é a situação de um vendedor quanto à venda de e-books.
type DigitalRights struct {
	SellerID       int
	SellerName     string
	SellerEmail    string
	SellerDocument string
	Status         string // "", pendente, aprovado, recusado
	Kind           string // autor ou editora
	Authors        []string
	Request        string // a comprovação enviada pelo vendedor
	Note           string // observação do admin (motivo da recusa, por exemplo)
	UpdatedAt      string
}

func (d DigitalRights) Approved() bool { return d.Status == DigitalApproved }
func (d DigitalRights) Pending() bool  { return d.Status == DigitalPending }

func (d DigitalRights) KindLabel() string {
	switch d.Kind {
	case DigitalKindAuthor:
		return "Autor(a) independente"
	case DigitalKindPublisher:
		return "Editora ou distribuidora parceira"
	}
	return ""
}

// BadgeLabel é o selo mostrado ao comprador na página do e-book.
func (d DigitalRights) BadgeLabel() string {
	if !d.Approved() {
		return ""
	}
	if d.Kind == DigitalKindAuthor {
		return "Publicado pelo autor"
	}
	return "Editora parceira"
}

func (d DigitalRights) StatusLabel() string {
	switch d.Status {
	case DigitalPending:
		return "Em análise"
	case DigitalApproved:
		return "Aprovado"
	case DigitalRejected:
		return "Recusado"
	}
	return "Não solicitado"
}

// AuthorsText junta os nomes, um por linha, para os formulários.
func (d DigitalRights) AuthorsText() string { return strings.Join(d.Authors, "\n") }

// AllowsAuthor diz se o e-book com esse autor pode ser vendido por este
// vendedor: editora parceira publica qualquer autor; autor(a), só os nomes
// aprovados.
func (d DigitalRights) AllowsAuthor(author string) bool {
	if !d.Approved() {
		return false
	}
	if d.Kind == DigitalKindPublisher {
		return true
	}
	alvo := normalizeName(author)
	for _, a := range d.Authors {
		if alvo != "" && normalizeName(a) == alvo {
			return true
		}
	}
	return false
}

func normalizeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// parseAuthors limpa a lista de nomes: um por linha, sem repetidos.
func parseAuthors(raw []string) []string {
	var out []string
	vistos := map[string]bool{}
	for _, linha := range raw {
		for _, nome := range strings.Split(linha, "\n") {
			nome = strings.Join(strings.Fields(nome), " ")
			if nome == "" || vistos[normalizeName(nome)] {
				continue
			}
			if len([]rune(nome)) > 120 {
				nome = string([]rune(nome)[:120])
			}
			vistos[normalizeName(nome)] = true
			out = append(out, nome)
		}
	}
	return out
}

func scanDigitalRights(scan func(...any) error) (DigitalRights, error) {
	var d DigitalRights
	var authors string
	err := scan(&d.SellerID, &d.SellerName, &d.SellerEmail, &d.SellerDocument,
		&d.Status, &d.Kind, &authors, &d.Request, &d.Note, &d.UpdatedAt)
	d.Authors = parseAuthors([]string{authors})
	return d, err
}

const digitalColumns = `id, name, email, document, digital_status, digital_kind, digital_authors, digital_request, digital_note, digital_updated_at`

// GetDigitalRights devolve a situação do vendedor. A loja (0) é sempre
// aprovada como editora.
func GetDigitalRights(sellerID int) DigitalRights {
	if sellerID == 0 {
		return DigitalRights{Status: DigitalApproved, Kind: DigitalKindPublisher}
	}
	d, err := scanDigitalRights(database.DB.QueryRow(`SELECT `+digitalColumns+` FROM sellers WHERE id=?`, sellerID).Scan)
	if err != nil {
		return DigitalRights{SellerID: sellerID}
	}
	return d
}

// RequestDigitalRights registra o pedido do vendedor para vender e-books.
func RequestDigitalRights(sellerID int, kind string, authors []string, request string) error {
	if kind != DigitalKindAuthor && kind != DigitalKindPublisher {
		return fmt.Errorf("%w: escolha se você é autor(a) ou editora", ErrDigitalRequest)
	}
	nomes := parseAuthors(authors)
	if kind == DigitalKindAuthor && len(nomes) == 0 {
		return fmt.Errorf("%w: informe o nome com que você publica", ErrDigitalRequest)
	}
	if len(nomes) > maxDigitalAuthors {
		return fmt.Errorf("%w: informe no máximo %d nomes", ErrDigitalRequest, maxDigitalAuthors)
	}
	request = strings.TrimSpace(request)
	if n := len([]rune(request)); n < 30 || n > 3000 {
		return fmt.Errorf("%w: conte como comprovar os direitos (de 30 a 3000 caracteres)", ErrDigitalRequest)
	}
	if GetDigitalRights(sellerID).Approved() {
		return fmt.Errorf("%w: sua venda de e-books já está aprovada; para mudar, fale com a loja", ErrDigitalRequest)
	}
	_, err := database.DB.Exec(`UPDATE sellers SET digital_status=?, digital_kind=?, digital_authors=?, digital_request=?,
		digital_note='', digital_updated_at=? WHERE id=?`,
		DigitalPending, kind, strings.Join(nomes, "\n"), request, time.Now().Format("2006-01-02 15:04"), sellerID)
	return err
}

// ReviewDigitalRights aprova (com a lista final de nomes, que o admin pode
// corrigir) ou recusa/revoga, com uma observação para o vendedor.
func ReviewDigitalRights(sellerID int, approve bool, authors []string, note string) error {
	d := GetDigitalRights(sellerID)
	if d.Status == "" {
		return fmt.Errorf("%w: vendedor sem pedido", ErrDigitalRequest)
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 1000 {
		note = string([]rune(note)[:1000])
	}
	status := DigitalRejected
	nomes := d.Authors
	if approve {
		status = DigitalApproved
		nomes = parseAuthors(authors)
		if d.Kind == DigitalKindAuthor && len(nomes) == 0 {
			return fmt.Errorf("%w: autor(a) precisa de pelo menos um nome aprovado", ErrDigitalRequest)
		}
	} else if note == "" {
		return fmt.Errorf("%w: explique o motivo para o vendedor", ErrDigitalRequest)
	}
	_, err := database.DB.Exec(`UPDATE sellers SET digital_status=?, digital_authors=?, digital_note=?, digital_updated_at=? WHERE id=?`,
		status, strings.Join(nomes, "\n"), note, time.Now().Format("2006-01-02 15:04"), sellerID)
	return err
}

// ListDigitalRights lista os pedidos, os pendentes primeiro. status vazio = todos.
func ListDigitalRights(status string) ([]DigitalRights, error) {
	query := `SELECT ` + digitalColumns + ` FROM sellers WHERE digital_status <> ''`
	args := []any{}
	if status != "" {
		query += ` AND digital_status=?`
		args = append(args, status)
	}
	query += ` ORDER BY CASE digital_status WHEN 'pendente' THEN 0 WHEN 'aprovado' THEN 1 ELSE 2 END, digital_updated_at DESC`
	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DigitalRights
	for rows.Next() {
		d, err := scanDigitalRights(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountPendingDigital conta os pedidos esperando análise (contador do menu).
func CountPendingDigital() int {
	var n int
	database.DB.QueryRow(`SELECT COUNT(*) FROM sellers WHERE digital_status=?`, DigitalPending).Scan(&n)
	return n
}

// checkDigitalRights é a trava do cadastro de e-book, conferida no modelo
// para valer em qualquer caminho (painel do vendedor, do admin ou POST forjado).
func checkDigitalRights(p *Product) error {
	if p.Format != FormatDigital {
		return nil
	}
	d := GetDigitalRights(p.SellerID)
	if !d.Approved() {
		return fmt.Errorf("%w: a venda de e-books precisa ser aprovada pela loja (em Livros digitais, no painel)", ErrInvalidProduct)
	}
	if !d.AllowsAuthor(p.Author) {
		return fmt.Errorf("%w: como autor(a), você só vende e-books com os nomes aprovados (%s)",
			ErrInvalidProduct, strings.Join(d.Authors, ", "))
	}
	if p.EbookSHA256 != "" {
		var outro int
		database.DB.QueryRow(`SELECT id FROM products WHERE ebook_sha256=? AND id<>? LIMIT 1`, p.EbookSHA256, p.ID).Scan(&outro)
		if outro != 0 {
			return fmt.Errorf("%w: este arquivo já está cadastrado na loja; o mesmo e-book não pode ser vendido duas vezes", ErrInvalidProduct)
		}
	}
	return nil
}

// sellerSellsDigital diz se o vendedor pode vender e-books agora (o checkout
// confere de novo, caso a aprovação tenha sido revogada depois de o livro ir
// para a sacola).
func sellerSellsDigital(sellerID int) bool {
	return GetDigitalRights(sellerID).Approved()
}
