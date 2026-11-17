package models

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vendas/loja/database"
)

// Denúncias contra vendedores. O nome e o e-mail de quem denunciou aparecem
// apenas para o admin; o vendedor nunca vê quem o denunciou.

var ErrInvalidReport = errors.New("denúncia inválida")

// ReportReasons são os motivos aceitos, com o texto mostrado no formulário.
var ReportReasons = []struct {
	Key   string
	Label string
}{
	{"fraude", "Golpe ou fraude"},
	{"direitos_autorais", "Violação de direitos autorais (pirataria)"},
	{"nao_entregue", "Paguei e não recebi"},
	{"diferente", "Produto diferente do anunciado"},
	{"fora_do_site", "Pediu pagamento fora do site"},
	{"comportamento", "Comportamento abusivo"},
	{"outro", "Outro motivo"},
}

var ReportStatuses = []struct {
	Key   string
	Label string
}{
	{"aberta", "Aberta"},
	{"em_analise", "Em análise"},
	{"resolvida", "Resolvida"},
	{"descartada", "Descartada"},
}

func reportReasonLabel(key string) string {
	for _, r := range ReportReasons {
		if r.Key == key {
			return r.Label
		}
	}
	return ""
}

func validReportStatus(key string) bool {
	for _, s := range ReportStatuses {
		if s.Key == key {
			return true
		}
	}
	return false
}

type Report struct {
	ID            int
	SellerID      int
	SellerName    string
	SellerActive  bool
	CustomerName  string
	CustomerEmail string
	OrderID       int
	ProductID     int // livro denunciado, se houver
	ProductName   string
	ProductActive bool
	Reason        string
	Description   string
	Status        string
	AdminNote     string
	CreatedAt     string
}

func (r Report) ReasonLabel() string { return reportReasonLabel(r.Reason) }

func (r Report) StatusLabel() string {
	for _, s := range ReportStatuses {
		if s.Key == r.Status {
			return s.Label
		}
	}
	return r.Status
}

// CreateReport registra a denúncia. Se vier um pedido, ele precisa ser do
// próprio cliente e do vendedor denunciado; se vier um livro, ele precisa ser
// desse vendedor.
func CreateReport(sellerID, customerID, orderID, productID int, reason, description string) error {
	description = strings.TrimSpace(description)
	if reportReasonLabel(reason) == "" {
		return fmt.Errorf("%w: escolha um motivo", ErrInvalidReport)
	}
	if n := len([]rune(description)); n < 20 || n > 2000 {
		return fmt.Errorf("%w: descreva o que aconteceu em 20 a 2000 caracteres", ErrInvalidReport)
	}
	if _, err := GetSeller(sellerID); err != nil || sellerID <= 0 {
		return fmt.Errorf("%w: vendedor não encontrado", ErrInvalidReport)
	}
	if orderID > 0 {
		order, err := GetOrder(orderID)
		if err != nil || order.CustomerID != customerID || order.SellerID != sellerID {
			return fmt.Errorf("%w: pedido não corresponde", ErrInvalidReport)
		}
	}
	if productID > 0 {
		var owner int
		if err := database.DB.QueryRow(`SELECT seller_id FROM products WHERE id=?`, productID).Scan(&owner); err != nil || owner != sellerID {
			return fmt.Errorf("%w: livro não corresponde ao vendedor", ErrInvalidReport)
		}
	}
	_, err := database.DB.Exec(
		`INSERT INTO seller_reports (seller_id, customer_id, order_id, product_id, reason, description) VALUES (?, ?, ?, ?, ?, ?)`,
		sellerID, customerID, orderID, productID, reason, description,
	)
	return err
}

// ListReports lista as denúncias, as abertas primeiro. status vazio = todas.
func ListReports(status string) ([]Report, error) {
	query := `SELECT r.id, r.seller_id, COALESCE(s.name, ''), COALESCE(s.active, 0), c.name, c.email,
		r.order_id, r.product_id, COALESCE(p.name, ''), COALESCE(p.active, 0),
		r.reason, r.description, r.status, r.admin_note, r.created_at
		FROM seller_reports r
		LEFT JOIN sellers s ON s.id = r.seller_id
		LEFT JOIN products p ON p.id = r.product_id AND r.product_id > 0
		JOIN customers c ON c.id = r.customer_id`
	args := []any{}
	if validReportStatus(status) {
		query += ` WHERE r.status=?`
		args = append(args, status)
	}
	query += ` ORDER BY CASE r.status WHEN 'aberta' THEN 0 WHEN 'em_analise' THEN 1 ELSE 2 END, r.id DESC`
	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reports []Report
	for rows.Next() {
		var rep Report
		var active, productActive int
		if err := rows.Scan(&rep.ID, &rep.SellerID, &rep.SellerName, &active, &rep.CustomerName, &rep.CustomerEmail,
			&rep.OrderID, &rep.ProductID, &rep.ProductName, &productActive,
			&rep.Reason, &rep.Description, &rep.Status, &rep.AdminNote, &rep.CreatedAt); err != nil {
			return nil, err
		}
		rep.SellerActive = active == 1
		rep.ProductActive = productActive == 1
		reports = append(reports, rep)
	}
	return reports, rows.Err()
}

func UpdateReport(id int, status, note string) error {
	if !validReportStatus(status) {
		return ErrInvalidReport
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 1000 {
		note = string([]rune(note)[:1000])
	}
	_, err := database.DB.Exec(`UPDATE seller_reports SET status=?, admin_note=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, status, note, id)
	return err
}

// CountOpenReports conta denúncias abertas ou em análise de um vendedor (ou
// de todos, com sellerID 0).
func CountOpenReports(sellerID int) int {
	var n int
	if sellerID > 0 {
		database.DB.QueryRow(`SELECT COUNT(*) FROM seller_reports WHERE seller_id=? AND status IN ('aberta','em_analise')`, sellerID).Scan(&n)
	} else {
		database.DB.QueryRow(`SELECT COUNT(*) FROM seller_reports WHERE status IN ('aberta','em_analise')`).Scan(&n)
	}
	return n
}

// SetSellerActive suspende ou reativa um vendedor. Suspenso, ele não entra no
// painel e os livros dele somem da vitrine.
func SetSellerActive(sellerID int, active bool) error {
	_, err := database.DB.Exec(`UPDATE sellers SET active=? WHERE id=?`, boolToInt(active), sellerID)
	return err
}
