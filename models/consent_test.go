package models

import (
	"errors"
	"testing"

	"github.com/vendas/loja/database"
)

func TestAceiteDosTermosFicaRegistrado(t *testing.T) {
	setupDB(t)
	id, err := CreateCustomer("Ana", "ana@exemplo.com", "senha-com-12-caracteres")
	if err != nil {
		t.Fatal(err)
	}
	if HasCurrentConsent(SubjectCustomer, int(id)) {
		t.Fatal("aceite sem registro")
	}
	if err := RecordConsent(SubjectCustomer, int(id), "203.0.113.9", "Navegador", "cadastro"); err != nil {
		t.Fatal(err)
	}
	if !HasCurrentConsent(SubjectCustomer, int(id)) || HasCurrentConsent(SubjectSeller, int(id)) {
		t.Fatal("aceite do cliente confundido com o de vendedor")
	}
	// Versão antiga não vale: quem aceitou só a anterior aceita de novo.
	database.DB.Exec(`UPDATE consents SET version='2020-01-01'`)
	if HasCurrentConsent(SubjectCustomer, int(id)) {
		t.Fatal("aceite de versão antiga vale para a nova")
	}
	if c := ListConsents(SubjectCustomer, int(id)); len(c) != 1 || c[0].IP != "203.0.113.9" || c[0].Origin != "cadastro" {
		t.Fatalf("histórico = %+v", c)
	}
}

func TestExclusaoDaContaApagaDadosEGuardaPedidosPagos(t *testing.T) {
	setupDB(t)
	id, _ := CreateCustomer("Bruno Lima", "bruno@exemplo.com", "senha-com-12-caracteres")
	cid := int(id)
	livro := criaProduto(t, 30, 5)
	pago := pedidoCom(livro)
	pago.CustomerID, pago.CustomerName, pago.CustomerEmail = cid, "Bruno Lima", "bruno@exemplo.com"
	pagoID, _ := CreateOrder(pago)
	MarkOrderPaid(int(pagoID), 3000, "pix")
	pendente := pedidoCom(livro)
	pendente.CustomerID, pendente.CustomerName, pendente.CustomerEmail = cid, "Bruno Lima", "bruno@exemplo.com"
	pendenteID, _ := CreateOrder(pendente)

	// Pedido pago ainda não entregue segura a exclusão.
	if _, err := DeleteCustomerAccount(cid); !errors.Is(err, ErrOpenOrders) {
		t.Fatalf("excluiu com pedido em andamento: %v", err)
	}
	UpdateOrderStatus(int(pagoID), "entregue")

	export, err := CustomerDataExport(cid)
	if err != nil || len(export["pedidos"].([]map[string]any)) != 2 {
		t.Fatalf("exportação: %v %v", err, export)
	}
	if _, err := DeleteCustomerAccount(cid); err != nil {
		t.Fatal(err)
	}
	c, _ := GetCustomerByID(cid)
	if c.Name != "Conta excluída" || c.Email == "bruno@exemplo.com" || c.Phone != "" {
		t.Fatalf("dados da conta ficaram: %+v", c)
	}
	if _, err := AuthenticateCustomer("bruno@exemplo.com", "senha-com-12-caracteres"); err == nil {
		t.Fatal("login continua funcionando depois da exclusão")
	}
	// O e-mail fica livre para um cadastro novo.
	if _, err := CreateCustomer("Bruno", "bruno@exemplo.com", "senha-com-12-caracteres"); err != nil {
		t.Fatalf("e-mail continua preso: %v", err)
	}
	o, _ := GetOrder(int(pagoID))
	if o.CustomerName != "Bruno Lima" || o.CustomerID != 0 {
		t.Fatalf("pedido pago = %q / cliente %d; quer guardado e sem vínculo", o.CustomerName, o.CustomerID)
	}
	if p, _ := GetOrder(int(pendenteID)); p.CustomerEmail != "" {
		t.Fatal("pedido nunca pago manteve o e-mail")
	}
}
