package models

import "testing"

func TestBoasVindasSoUmaVezEContasExcluidasFora(t *testing.T) {
	setupDB(t)
	CreateCustomer("Ana", "Ana@Exemplo.com", "senha-com-12-caracteres")
	id, _ := CreateCustomer("Bia", "bia@exemplo.com", "senha-com-12-caracteres")
	CreateSeller("Sebo Um", "seboum", "sebo@exemplo.com", cpfTeste1, "senha-com-12-caracteres")
	DeleteCustomerAccount(int(id))

	if c, v := CountPendingWelcomes(); c != 1 || v != 1 {
		t.Fatalf("pendentes = %d clientes, %d vendedores; quer 1 e 1 (conta excluída fora)", c, v)
	}
	MarkWelcomeSent(false, "ana@exemplo.com") // maiúsculas não importam
	MarkWelcomeSent(true, "sebo@exemplo.com")
	if list, _ := PendingWelcomes(); len(list) != 0 {
		t.Fatalf("ainda pendentes: %+v", list)
	}
}
