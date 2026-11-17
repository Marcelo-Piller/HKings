package services

import (
	"bytes"
	"encoding/json"
	"net/mail"
	"strings"
	"testing"
)

func TestPickInstallmentPlan(t *testing.T) {
	// Resposta real do Mercado Pago para R$ 79,90, resumida.
	bruto := `[{"payer_costs":[
		{"installments":1,"installment_rate":0,"installment_amount":79.9,"total_amount":79.9},
		{"installments":12,"installment_rate":22.11,"installment_amount":8.13,"total_amount":97.57},
		{"installments":15,"installment_rate":25.83,"installment_amount":6.7,"total_amount":100.54}]}]`
	var parsed installmentsResponse
	if err := json.Unmarshal([]byte(bruto), &parsed); err != nil {
		t.Fatal(err)
	}
	plano := pickInstallmentPlan(parsed, 12)
	if plano == nil || plano.Installments != 12 || plano.Amount != 8.13 || plano.Total != 97.57 || plano.InterestFree {
		t.Fatalf("plano = %+v; quer 12x de 8,13 com juros (15x não pode passar do limite)", plano)
	}

	semJuros := `[{"payer_costs":[{"installments":3,"installment_rate":0,"installment_amount":10,"total_amount":30}]}]`
	json.Unmarshal([]byte(semJuros), &parsed)
	if p := pickInstallmentPlan(parsed, 12); p == nil || !p.InterestFree {
		t.Fatalf("parcelamento sem juros não reconhecido: %+v", p)
	}
}

func TestReceiptPDFGeraArquivoValido(t *testing.T) {
	pdf, err := ReceiptPDF(Receipt{
		StoreName: "Livredo", OrderID: 42, CreatedAt: "25/09/2026 às 14:20", PaidAt: "25/09/2026 às 14:26",
		PaymentMethod: "PIX", CustomerName: "João Conceição", CustomerEmail: "joao@exemplo.com",
		Address: "Rua São João, 100 — Centro, São Paulo", SellerName: "Sebo Estante Viva", SellerKind: "Loja",
		Items: []ReceiptItem{{Title: "Memórias Póstumas de Brás Cubas", Quantity: 2, UnitPrice: "R$ 42,90", Subtotal: "R$ 85,80"}},
		Total: "R$ 85,80",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 1000 {
		t.Fatalf("saída não parece um PDF (%d bytes)", len(pdf))
	}
}

func TestEmailRecusaInjecaoDeCabecalho(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.exemplo.com", Port: 587, User: "loja@exemplo.com", Password: "x", FromName: "Loja"}
	// Um assunto com quebra de linha poderia injetar um Bcc escondido.
	err := SendEmail(cfg, Email{To: "cliente@exemplo.com", Subject: "Oi\r\nBcc: vitima@exemplo.com"})
	if err == nil || !strings.Contains(err.Error(), "quebra de linha") {
		t.Fatalf("assunto com CRLF aceito: %v", err)
	}
	if err := SendEmail(cfg, Email{To: "não é e-mail", Subject: "x"}); err == nil {
		t.Fatal("destinatário inválido aceito")
	}
	if err := SendEmail(SMTPConfig{}, Email{To: "a@b.com"}); err == nil {
		t.Fatal("enviou sem configuração")
	}
}

func TestMIMEComAnexoEAcentos(t *testing.T) {
	msg, err := buildMIME(
		mail.Address{Name: "Livredo", Address: "loja@exemplo.com"},
		mail.Address{Address: "cliente@exemplo.com"},
		Email{Subject: "Comprovante do pedido nº 7", TextBody: "Olá", HTMLBody: "<p>Olá</p>",
			Attachments: []EmailAttachment{{Filename: "comprovante.pdf", ContentType: "application/pdf", Data: bytes.Repeat([]byte("x"), 300)}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	texto := string(msg)
	for _, parte := range []string{"Subject: =?utf-8?q?", "multipart/mixed", `filename="comprovante.pdf"`, "application/pdf"} {
		if !strings.Contains(texto, parte) {
			t.Errorf("mensagem sem %q", parte)
		}
	}
	for _, linha := range strings.Split(texto, "\r\n") {
		if len(linha) > 998 {
			t.Fatal("linha acima do limite do SMTP")
		}
	}
}
