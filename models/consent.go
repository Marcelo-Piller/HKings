package models

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/vendas/loja/database"
	"golang.org/x/crypto/bcrypt"
)

// Termos de Uso e Política de Privacidade (LGPD, Lei 13.709/2018).
//
// Cada aceite é gravado em consents, sem nunca ser apagado nem alterado: é a
// prova de que a pessoa concordou (art. 8º, §2º: o ônus da prova é do
// controlador), com data, versão dos documentos, IP e navegador. Quando os
// textos mudam, TermsVersion muda e todos aceitam de novo no próximo acesso.

// TermsVersion identifica a versão dos Termos e da Política em vigor. Mude a
// data sempre que alterar o conteúdo de templates/terms.html ou privacy.html.
const TermsVersion = "2026-10-02"

// Quem aceita.
const (
	SubjectCustomer = "cliente"
	SubjectSeller   = "vendedor"
)

// ErrTermsRequired: o formulário veio sem a caixa de aceite marcada.
var ErrTermsRequired = errors.New("é preciso aceitar os Termos de Uso e a Política de Privacidade")

// Consent é um aceite registrado.
type Consent struct {
	Version    string
	AcceptedAt string
	IP         string
	Origin     string // cadastro, login, checkout…
}

// RecordConsent grava o aceite da versão atual.
func RecordConsent(subject string, subjectID int, ip, userAgent, origin string) error {
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	_, err := database.DB.Exec(`INSERT INTO consents (subject, subject_id, version, ip, user_agent, origin) VALUES (?,?,?,?,?,?)`,
		subject, subjectID, TermsVersion, ip, userAgent, origin)
	return err
}

// HasCurrentConsent diz se a pessoa já aceitou a versão em vigor.
func HasCurrentConsent(subject string, subjectID int) bool {
	var n int
	database.DB.QueryRow(`SELECT COUNT(*) FROM consents WHERE subject=? AND subject_id=? AND version=?`,
		subject, subjectID, TermsVersion).Scan(&n)
	return n > 0
}

// ListConsents é o histórico de aceites da pessoa, do mais recente ao mais antigo.
func ListConsents(subject string, subjectID int) []Consent {
	rows, err := database.DB.Query(`SELECT version, accepted_at, ip, origin FROM consents
		WHERE subject=? AND subject_id=? ORDER BY id DESC`, subject, subjectID)
	out := []Consent{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c Consent
		if rows.Scan(&c.Version, &c.AcceptedAt, &c.IP, &c.Origin) == nil {
			out = append(out, c)
		}
	}
	return out
}

// ── Direitos do titular (art. 18) ────────────────────────────────────────────

// CustomerDataExport são os dados pessoais do cliente, para download.
func CustomerDataExport(customerID int) (map[string]any, error) {
	c, err := GetCustomerByID(customerID)
	if err != nil {
		return nil, err
	}
	orders, _ := ListOrdersByCustomer(customerID)
	var pedidos []map[string]any
	for _, o := range orders {
		full, err := GetOrder(o.ID)
		if err != nil {
			continue
		}
		var itens []map[string]any
		for _, it := range full.Items {
			itens = append(itens, map[string]any{"livro": it.ProductName, "quantidade": it.Quantity, "preco": it.Price})
		}
		pedidos = append(pedidos, map[string]any{
			"numero": full.ID, "data": full.CreatedAt, "situacao": full.Status, "total": full.Total,
			"nome": full.CustomerName, "email": full.CustomerEmail, "telefone": full.CustomerPhone,
			"endereco": full.Address, "documento": full.CustomerDocument, "itens": itens,
		})
	}
	return map[string]any{
		"conta": map[string]any{
			"id": c.ID, "nome": c.Name, "email": c.Email, "telefone": c.Phone, "endereco": c.Address,
			"foto": c.AvatarURL, "login_google": c.GoogleSub != "", "criada_em": c.CreatedAt,
		},
		"pedidos": pedidos,
		"aceites": ListConsents(SubjectCustomer, customerID),
	}, nil
}

// ErrOpenOrders: a conta tem pedido em andamento e ainda não pode ser excluída.
var ErrOpenOrders = errors.New("há pedidos em andamento")

// DeleteCustomerAccount exclui a conta do cliente (art. 18, VI): apaga nome,
// e-mail, telefone, endereço, foto, senha e o vínculo com o Google. Os
// pedidos ficam, sem ligação com a conta, porque a lei fiscal e o Código de
// Defesa do Consumidor obrigam a guardá-los (art. 16, I). E-books comprados
// deixam de estar disponíveis. Devolve a foto, para quem chama apagar o arquivo.
func DeleteCustomerAccount(customerID int) (avatar string, err error) {
	var open int
	database.DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE customer_id=? AND status IN ('pago','pronto','enviado')`, customerID).Scan(&open)
	if open > 0 {
		return "", ErrOpenOrders
	}
	database.DB.QueryRow(`SELECT avatar_url FROM customers WHERE id=?`, customerID).Scan(&avatar)
	var raw [16]byte
	rand.Read(raw[:])
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(raw[:])), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE customers SET name='Conta excluída', email=?, password_hash=?, phone='', address='',
		avatar_url='', google_sub='' WHERE id=?`,
		"excluida-"+hex.EncodeToString(raw[:6])+"@removido.invalid", string(hash), customerID); err != nil {
		return "", err
	}
	// Pedidos pendentes nunca pagos não precisam ser guardados com dados pessoais.
	if _, err := tx.Exec(`UPDATE orders SET customer_name='Conta excluída', customer_email='', customer_phone='', address='',
		customer_document='', ship_street='', ship_number='', ship_complement='', ship_district='', ship_city='', ship_state='', ship_postal_code=''
		WHERE customer_id=? AND status IN ('pendente','aguardando_pagamento','cancelado') AND paid_amount_cents=0`, customerID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE orders SET customer_id=0 WHERE customer_id=?`, customerID); err != nil {
		return "", err
	}
	if !strings.HasPrefix(avatar, "/static/img/uploads/") {
		avatar = ""
	}
	return avatar, tx.Commit()
}

// SetOrderTermsVersion grava no pedido a versão dos termos aceita na compra.
func SetOrderTermsVersion(orderID int) {
	database.DB.Exec(`UPDATE orders SET terms_version=? WHERE id=?`, TermsVersion, orderID)
}
